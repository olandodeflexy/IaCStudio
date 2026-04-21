package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/iac-studio/iac-studio/internal/ai/providers"
	"github.com/iac-studio/iac-studio/internal/ai/tools"
)

// TestRouteKeywordMatching — each specialist wins for prompts that contain
// its keywords, and ambiguous prompts default to architect.
func TestRouteKeywordMatching(t *testing.T) {
	specs := DefaultSpecialists()
	cases := []struct {
		prompt string
		want   string
	}{
		{"add a new VPC with three subnets", "architect"},
		{"fix the s3 bucket tags to match policy", "policy"},
		{"audit my infrastructure for security vulnerabilities", "security"},
		{"review the plan before we apply", "reviewer"},
		{"change the cidr_block to 10.1.0.0/16", "coder"},
		{"hello", "architect"}, // ambiguous falls through to architect
	}
	for _, tc := range cases {
		got := Route(tc.prompt, specs)
		if got.Name != tc.want {
			t.Errorf("Route(%q) → %q, want %q", tc.prompt, got.Name, tc.want)
		}
	}
}

// TestAdaptConvertsBothWays — the adapter must round-trip every field a
// provider cares about: call ID, name, args JSON, result content, error flag.
func TestAdaptConvertsBothWays(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.New("echo", "echoes", `{}`,
		func(ctx context.Context, args json.RawMessage) (any, error) {
			return map[string]string{"saw": string(args)}, nil
		}))
	reg.Register(tools.New("fail", "errors", `{}`,
		func(ctx context.Context, args json.RawMessage) (any, error) {
			return nil, errors.New("kaboom")
		}))

	runner := &tools.Runner{Registry: reg, MaxIterations: 10}
	adapter := Adapt(runner)

	in := []providers.ToolCall{
		{ID: "1", Name: "echo", Args: []byte(`{"hi":true}`)},
		{ID: "2", Name: "fail", Args: []byte(`{}`)},
	}
	out, err := adapter.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2 results, got %d", len(out))
	}
	if out[0].CallID != "1" || out[1].CallID != "2" {
		t.Errorf("CallIDs lost through adapter: %+v", out)
	}
	if out[1].IsError != true {
		t.Errorf("error flag must propagate, got %+v", out[1])
	}
}

// mockToolUser stands in for providers.ToolUser — lets us verify the
// orchestrator narrows the tool set to the specialist's allow-list and
// passes the specialist's system prompt verbatim.
type mockToolUser struct {
	sawSystem string
	sawTools  []string
	reply     string
}

func (m *mockToolUser) Kind() providers.Kind                   { return providers.KindAnthropic }
func (m *mockToolUser) Complete(context.Context, providers.Request) (string, error) {
	return "", nil
}
func (m *mockToolUser) Stream(context.Context, providers.Request, providers.DeltaFunc) (string, error) {
	return "", nil
}
func (m *mockToolUser) RunToolLoop(ctx context.Context, req providers.ToolLoopRequest) (string, error) {
	m.sawSystem = req.System
	m.sawTools = nil
	for _, t := range req.Tools {
		m.sawTools = append(m.sawTools, t.Name)
	}
	return m.reply, nil
}

// TestRunPolicyPromptPicksPolicySpecialistAndNarrowsTools — the canonical
// integration test: a policy-flavoured prompt routes to the policy
// specialist, and the provider only sees the policy specialist's allowed
// tools (no run_scan, no search_registry).
func TestRunPolicyPromptPicksPolicySpecialistAndNarrowsTools(t *testing.T) {
	reg := tools.NewRegistry()
	// Register stubs under every tool name the specialists reference so the
	// allow-list filter has something to match.
	for _, name := range []string{
		"list_resources", "get_resource", "search_registry", "write_hcl",
		"run_policy", "run_scan", "read_plan",
	} {
		reg.Register(tools.New(name, name, `{}`,
			func(ctx context.Context, args json.RawMessage) (any, error) { return nil, nil }))
	}
	provider := &mockToolUser{reply: "policy fixed"}

	result, err := Run(context.Background(), Config{
		Provider:     provider,
		ToolRegistry: reg,
	}, "please audit my resources against our tagging policy")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Specialist != "policy" {
		t.Errorf("should route to policy, got %q", result.Specialist)
	}
	if result.Reply != "policy fixed" {
		t.Errorf("reply lost: %q", result.Reply)
	}
	if !strings.Contains(provider.sawSystem, "Policy sub-agent") {
		t.Errorf("specialist system prompt not used: %q", provider.sawSystem)
	}
	wantTools := map[string]bool{"list_resources": true, "get_resource": true, "run_policy": true, "write_hcl": true}
	if len(provider.sawTools) != len(wantTools) {
		t.Errorf("policy specialist should see exactly 4 tools, got %v", provider.sawTools)
	}
	for _, name := range provider.sawTools {
		if !wantTools[name] {
			t.Errorf("tool %q leaked to policy specialist", name)
		}
	}
}

// TestRunRejectsNonToolUserProvider — Ollama doesn't implement ToolUser;
// the orchestrator must return a clear error rather than panic.
func TestRunRejectsNonToolUserProvider(t *testing.T) {
	_, err := Run(context.Background(), Config{
		Provider:     &plainProvider{},
		ToolRegistry: tools.NewRegistry(),
	}, "build me a VPC")
	if err == nil || !strings.Contains(err.Error(), "tool use") {
		t.Errorf("expected 'does not support tool use' error, got %v", err)
	}
}

type plainProvider struct{}

func (p *plainProvider) Kind() providers.Kind { return providers.KindOllama }
func (p *plainProvider) Complete(context.Context, providers.Request) (string, error) {
	return "", nil
}
func (p *plainProvider) Stream(context.Context, providers.Request, providers.DeltaFunc) (string, error) {
	return "", nil
}
