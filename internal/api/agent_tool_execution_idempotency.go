package api

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"sync"

	"github.com/iac-studio/iac-studio/internal/agentrouting"
	"github.com/iac-studio/iac-studio/internal/mcpairlock"
)

const maxAgentToolExecutionReplayEntries = 64

var (
	errAgentToolExecutionIdempotencyConflict = errors.New("idempotency key reused for a different tool execution")
	errAgentToolExecutionAttemptCapacity     = errors.New("tool execution idempotency capacity reached")
	errAgentToolExecutionRequiresReadOnly    = errors.New("tool execution idempotency supports read-only routes only")
)

type agentToolExecutionAttemptKey struct {
	runID string
	key   string
}

type agentToolExecutionAttempt struct {
	fingerprint [sha256.Size]byte
	done        chan struct{}
	decision    agentrouting.Decision
	invoked     bool
	result      mcpairlock.ToolCallResult
	hasResult   bool
	err         error
}

// agentToolExecutionAttemptStore coalesces concurrent retries and retains a
// bounded replay window for successful read-only tool executions. Write-side
// execution requires a durable approval and idempotency contract instead.
type agentToolExecutionAttemptStore struct {
	mu         sync.Mutex
	entries    map[agentToolExecutionAttemptKey]*agentToolExecutionAttempt
	completed  []agentToolExecutionAttemptKey
	maxEntries int
}

func newAgentToolExecutionAttemptStore(maxEntries int) *agentToolExecutionAttemptStore {
	if maxEntries <= 0 {
		maxEntries = 1
	}
	if maxEntries > maxAgentToolExecutionReplayEntries {
		maxEntries = maxAgentToolExecutionReplayEntries
	}
	return &agentToolExecutionAttemptStore{
		entries:    make(map[agentToolExecutionAttemptKey]*agentToolExecutionAttempt),
		maxEntries: maxEntries,
	}
}

func (s *agentToolExecutionAttemptStore) execute(
	ctx context.Context,
	runID string,
	idempotencyKey string,
	request agentrouting.Request,
	arguments mcpairlock.ToolCallArguments,
	execute func() (agentrouting.ExecutionResult, error),
) (agentrouting.ExecutionResult, bool, error) {
	if ctx == nil {
		return agentrouting.ExecutionResult{}, false, agentrouting.ErrToolExecutionContext
	}
	if err := ctx.Err(); err != nil {
		return agentrouting.ExecutionResult{}, false, err
	}
	if execute == nil {
		return agentrouting.ExecutionResult{}, false, agentrouting.ErrToolInvokerRequired
	}
	if request.Risk != mcpairlock.RiskReadOnly {
		return agentrouting.ExecutionResult{}, false, errAgentToolExecutionRequiresReadOnly
	}
	fingerprint, err := agentToolExecutionFingerprint(request, arguments)
	if err != nil {
		return agentrouting.ExecutionResult{}, false, err
	}
	key := agentToolExecutionAttemptKey{runID: runID, key: idempotencyKey}

	s.mu.Lock()
	if attempt, ok := s.entries[key]; ok {
		if attempt.fingerprint != fingerprint {
			s.mu.Unlock()
			return agentrouting.ExecutionResult{}, false, errAgentToolExecutionIdempotencyConflict
		}
		done := attempt.done
		s.mu.Unlock()

		select {
		case <-done:
			if attempt.err != nil {
				return agentrouting.ExecutionResult{}, true, attempt.err
			}
			return replayAgentToolExecution(attempt), true, nil
		case <-ctx.Done():
			return agentrouting.ExecutionResult{}, true, ctx.Err()
		}
	}
	if !s.makeRoomLocked() {
		s.mu.Unlock()
		return agentrouting.ExecutionResult{}, false, errAgentToolExecutionAttemptCapacity
	}
	attempt := &agentToolExecutionAttempt{
		fingerprint: fingerprint,
		done:        make(chan struct{}),
	}
	s.entries[key] = attempt
	s.mu.Unlock()

	result, err := execute()
	if err == nil {
		if validationErr := validateAgentToolExecutionResult(result); validationErr != nil {
			result = agentrouting.ExecutionResult{}
			err = validationErr
		}
	}

	s.mu.Lock()
	attempt.err = err
	if err == nil {
		attempt.decision = result.Route.Decision
		attempt.invoked = result.Invoked
		if result.Result != nil {
			attempt.result = *result.Result
			attempt.hasResult = true
		}
		s.completed = append(s.completed, key)
	} else {
		delete(s.entries, key)
	}
	close(attempt.done)
	s.mu.Unlock()

	return result, false, err
}

func validateAgentToolExecutionResult(result agentrouting.ExecutionResult) error {
	if err := result.Route.Decision.Validate(); err != nil {
		return agentrouting.ErrInvalidToolExecution
	}
	if result.Route.Decision.Status != agentrouting.DecisionAllowed {
		if result.Invoked || result.Result != nil {
			return agentrouting.ErrInvalidToolExecution
		}
		return nil
	}
	if !result.Invoked || result.Result == nil {
		return agentrouting.ErrInvalidToolExecution
	}
	if err := result.Result.Validate(); err != nil {
		return agentrouting.ErrInvalidToolExecution
	}
	return nil
}

func (s *agentToolExecutionAttemptStore) makeRoomLocked() bool {
	for len(s.entries) >= s.maxEntries && len(s.completed) > 0 {
		oldest := s.completed[0]
		s.completed = s.completed[1:]
		delete(s.entries, oldest)
	}
	return len(s.entries) < s.maxEntries
}

func replayAgentToolExecution(attempt *agentToolExecutionAttempt) agentrouting.ExecutionResult {
	result := agentrouting.ExecutionResult{
		Route: agentrouting.RouteResult{
			Decision: attempt.decision,
		},
		Invoked: attempt.invoked,
	}
	if attempt.hasResult {
		replayed := attempt.result
		result.Result = &replayed
	}
	return result
}

func agentToolExecutionFingerprint(
	request agentrouting.Request,
	arguments mcpairlock.ToolCallArguments,
) ([sha256.Size]byte, error) {
	if err := request.Validate(); err != nil {
		return [sha256.Size]byte{}, err
	}
	encodedArguments, err := arguments.MarshalJSON()
	if err != nil {
		return [sha256.Size]byte{}, err
	}

	hash := sha256.New()
	for _, value := range [][]byte{
		[]byte(request.Project),
		[]byte(request.ProviderID),
		[]byte(request.ConnectionID),
		[]byte(request.ServerID),
		[]byte(request.ToolName),
		[]byte(request.Mode),
		[]byte(request.Risk),
		encodedArguments,
	} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write(value)
	}

	var fingerprint [sha256.Size]byte
	copy(fingerprint[:], hash.Sum(nil))
	return fingerprint, nil
}
