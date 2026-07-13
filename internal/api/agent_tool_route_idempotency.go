package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/iac-studio/iac-studio/internal/agentrouting"
)

const (
	agentToolRouteIdempotencyHeader       = "Idempotency-Key"
	maxAgentToolRouteIdempotencyKeyLength = 128
	maxAgentToolRouteAttempts             = 1024
)

var (
	errAgentToolRouteIdempotencyConflict = errors.New("idempotency key reused for a different tool route")
	errAgentToolRouteAttemptCapacity     = errors.New("tool route idempotency capacity reached")
)

type agentToolRouteAttemptKey struct {
	runID string
	key   string
}

type agentToolRouteAttempt struct {
	request  agentrouting.Request
	done     chan struct{}
	decision agentrouting.Decision
	err      error
}

// agentToolRouteAttemptStore coalesces concurrent retries and retains a
// bounded replay window for successful, already-audited decisions.
type agentToolRouteAttemptStore struct {
	mu         sync.Mutex
	entries    map[agentToolRouteAttemptKey]*agentToolRouteAttempt
	completed  []agentToolRouteAttemptKey
	maxEntries int
}

func newAgentToolRouteAttemptStore(maxEntries int) *agentToolRouteAttemptStore {
	if maxEntries <= 0 {
		maxEntries = 1
	}
	return &agentToolRouteAttemptStore{
		entries:    make(map[agentToolRouteAttemptKey]*agentToolRouteAttempt),
		maxEntries: maxEntries,
	}
}

func (s *agentToolRouteAttemptStore) authorize(
	ctx context.Context,
	runID string,
	idempotencyKey string,
	request agentrouting.Request,
	route func() (agentrouting.RouteResult, error),
) (agentrouting.RouteResult, bool, error) {
	key := agentToolRouteAttemptKey{runID: runID, key: idempotencyKey}

	s.mu.Lock()
	if attempt, ok := s.entries[key]; ok {
		if attempt.request != request {
			s.mu.Unlock()
			return agentrouting.RouteResult{}, false, errAgentToolRouteIdempotencyConflict
		}
		done := attempt.done
		s.mu.Unlock()

		select {
		case <-done:
			if attempt.err != nil {
				return agentrouting.RouteResult{}, true, attempt.err
			}
			return agentrouting.RouteResult{Decision: attempt.decision}, true, nil
		case <-ctx.Done():
			return agentrouting.RouteResult{}, true, ctx.Err()
		}
	}
	if !s.makeRoomLocked() {
		s.mu.Unlock()
		return agentrouting.RouteResult{}, false, errAgentToolRouteAttemptCapacity
	}
	attempt := &agentToolRouteAttempt{request: request, done: make(chan struct{})}
	s.entries[key] = attempt
	s.mu.Unlock()

	result, err := route()

	s.mu.Lock()
	attempt.err = err
	if err == nil {
		attempt.decision = result.Decision
		s.completed = append(s.completed, key)
	} else {
		delete(s.entries, key)
	}
	close(attempt.done)
	s.mu.Unlock()

	return result, false, err
}

func (s *agentToolRouteAttemptStore) makeRoomLocked() bool {
	for len(s.entries) >= s.maxEntries && len(s.completed) > 0 {
		oldest := s.completed[0]
		s.completed = s.completed[1:]
		delete(s.entries, oldest)
	}
	return len(s.entries) < s.maxEntries
}

func requireAgentToolRouteIdempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	values := r.Header.Values(agentToolRouteIdempotencyHeader)
	if len(values) != 1 || !validAgentToolRouteIdempotencyKey(values[0]) {
		http.Error(w, "a valid Idempotency-Key header is required", http.StatusBadRequest)
		return "", false
	}
	return values[0], true
}

func validAgentToolRouteIdempotencyKey(key string) bool {
	if key == "" || len(key) > maxAgentToolRouteIdempotencyKeyLength || strings.TrimSpace(key) != key {
		return false
	}
	for i := range len(key) {
		if key[i] < 0x21 || key[i] > 0x7e {
			return false
		}
	}
	return true
}
