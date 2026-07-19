package agentrouting

import (
	"errors"
	"fmt"

	"github.com/iac-studio/iac-studio/internal/agentruns"
)

var (
	ErrAuthorizerRequired  = errors.New("tool route authorizer is required")
	ErrRunRecorderRequired = errors.New("tool route run recorder is required")
)

// RouteResult contains an authorization decision only after that decision has
// been recorded on the referenced Agent Run.
type RouteResult struct {
	Decision Decision      `json:"decision"`
	Run      agentruns.Run `json:"run"`
}

// Router coordinates authorization and Agent Run recording. It never invokes
// an external MCP tool or reads cloud credentials.
type Router struct {
	authorizer *Authorizer
	recorder   *RunRecorder
}

func NewRouter(authorizer *Authorizer, recorder *RunRecorder) (*Router, error) {
	if authorizer == nil {
		return nil, ErrAuthorizerRequired
	}
	if recorder == nil {
		return nil, ErrRunRecorderRequired
	}
	return &Router{authorizer: authorizer, recorder: recorder}, nil
}

// Preview evaluates one fully scoped tool route without recording the outcome
// or invoking an external MCP tool.
func (r *Router) Preview(request Request) (Decision, error) {
	if r == nil || r.authorizer == nil {
		return Decision{}, ErrAuthorizerRequired
	}
	decision := r.authorizer.Authorize(request)
	if err := decision.Validate(); err != nil {
		return Decision{}, fmt.Errorf("validate tool route preview: %w", err)
	}
	return decision, nil
}

// Route authorizes one fully scoped tool route and records the outcome before
// returning it. Recorder failures return a zero result so callers cannot act
// on an authorization decision that was not audited.
func (r *Router) Route(runID string, request Request) (RouteResult, error) {
	if r == nil || r.authorizer == nil {
		return RouteResult{}, ErrAuthorizerRequired
	}
	if r.recorder == nil {
		return RouteResult{}, ErrRunRecorderRequired
	}

	decision := r.authorizer.Authorize(request)
	run, err := r.recorder.Record(runID, request, decision)
	if err != nil {
		return RouteResult{}, fmt.Errorf("record tool route authorization: %w", err)
	}
	return RouteResult{Decision: decision, Run: run}, nil
}
