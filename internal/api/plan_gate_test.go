package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPlanGateRejectsStalePlan(t *testing.T) {
	projectDir := t.TempDir()
	t.Cleanup(func() { invalidatePlan(projectDir) })

	ref := recordCommandPlanAt(projectDir, "terraform", "", "", "plan", "old plan", time.Now().Add(-2*time.Hour))
	if _, rejection := validatePlanForCommand(projectDir, "terraform", "", "", "apply", ref.Hash); rejection == nil || rejection.Error != "plan_required" {
		t.Fatalf("expected stale plan_required rejection, got %#v", rejection)
	}
}

func TestPlanGateRejectsDifferentCommand(t *testing.T) {
	projectDir := t.TempDir()
	t.Cleanup(func() { invalidatePlan(projectDir) })

	ref := recordCommandPlan(projectDir, "terraform", "", "", "check", "syntax only")
	if _, rejection := validatePlanForCommand(projectDir, "terraform", "", "", "apply", ref.Hash); rejection == nil || rejection.Error != "plan_hash_mismatch" {
		t.Fatalf("expected command mismatch rejection, got %#v", rejection)
	}
}

func TestPlanGateRejectsDifferentEnvironment(t *testing.T) {
	projectDir := t.TempDir()
	t.Cleanup(func() { invalidatePlan(projectDir) })

	ref := recordCommandPlan(projectDir, "terraform", "dev", "", "plan", "dev plan")
	if _, rejection := validatePlanForCommand(projectDir, "terraform", "prod", "", "apply", ref.Hash); rejection == nil || rejection.Error != "plan_hash_mismatch" {
		t.Fatalf("expected environment mismatch rejection, got %#v", rejection)
	}
}

func TestPlanGateRejectsDifferentConnection(t *testing.T) {
	projectDir := t.TempDir()
	t.Cleanup(func() { invalidatePlan(projectDir) })

	ref := recordCommandPlan(projectDir, "terraform", "", "aws-dev", "plan", "dev account plan")
	if _, rejection := validatePlanForCommand(projectDir, "terraform", "", "aws-prod", "apply", ref.Hash); rejection == nil || rejection.Error != "plan_hash_mismatch" {
		t.Fatalf("expected connection mismatch rejection, got %#v", rejection)
	}
}

func TestPlanGateAcceptsMatchingPlanHash(t *testing.T) {
	projectDir := t.TempDir()
	t.Cleanup(func() { invalidatePlan(projectDir) })

	ref := recordCommandPlan(projectDir, "terraform", "dev", "aws-dev", "plan", "dev plan")
	if matched, rejection := validatePlanForCommand(projectDir, "terraform", "dev", "aws-dev", "apply", ref.Hash); rejection != nil {
		t.Fatalf("expected matching plan to pass, got %#v", rejection)
	} else if matched.Hash != ref.Hash {
		t.Fatalf("matched hash = %q, want %q", matched.Hash, ref.Hash)
	}
}

func TestConsumePlanRejectsPlanInvalidatedAfterValidation(t *testing.T) {
	projectDir := t.TempDir()
	t.Cleanup(func() { invalidatePlan(projectDir) })

	ref := recordCommandPlan(projectDir, "terraform", "dev", "aws-dev", "plan", "dev plan")
	if _, rejection := validatePlanForCommand(projectDir, "terraform", "dev", "aws-dev", "apply", ref.Hash); rejection != nil {
		t.Fatalf("expected initial validation to pass, got %#v", rejection)
	}

	invalidatePlan(projectDir)

	if _, rejection := consumePlanForCommand(projectDir, "terraform", "dev", "aws-dev", "apply", ref.Hash); rejection == nil || rejection.Error != "plan_required" {
		t.Fatalf("expected invalidated plan_required rejection, got %#v", rejection)
	}
}

func TestConsumePlanAllowsSingleUse(t *testing.T) {
	projectDir := t.TempDir()
	t.Cleanup(func() { invalidatePlan(projectDir) })

	ref := recordCommandPlan(projectDir, "terraform", "", "", "plan", "safe plan")
	if _, rejection := consumePlanForCommand(projectDir, "terraform", "", "", "apply", ref.Hash); rejection != nil {
		t.Fatalf("expected first consume to pass, got %#v", rejection)
	}
	if hasPlan(projectDir) {
		t.Fatal("plan gate should be removed after consume")
	}
	if _, rejection := consumePlanForCommand(projectDir, "terraform", "", "", "apply", ref.Hash); rejection == nil || rejection.Error != "plan_required" {
		t.Fatalf("expected second consume to fail, got %#v", rejection)
	}
}

func TestPlanGateInvalidatesChangedLayeredFile(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	envDir := filepath.Join(projectDir, "environments", "dev")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("mkdir env: %v", err)
	}
	t.Cleanup(func() { invalidatePlan(projectDir, envDir) })
	recordPlan(projectDir)
	recordPlan(envDir)

	invalidatePlanForChangedFile(root, filepath.Join(envDir, "main.tf"))

	if hasPlan(projectDir) {
		t.Fatal("root plan gate should be invalidated after watched file change")
	}
	if hasPlan(envDir) {
		t.Fatal("env plan gate should be invalidated after watched env file change")
	}
}

func TestRunCommandRequiresSubmittedPlanHash(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	t.Cleanup(func() { invalidatePlan(projectDir) })
	_ = recordCommandPlan(projectDir, "terraform", "", "", "plan", "safe plan")

	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	resp, err := http.Post(
		srv.URL+"/api/projects/demo/run",
		"application/json",
		strings.NewReader(`{"tool":"terraform","command":"apply","approved":true}`),
	)
	if err != nil {
		t.Fatalf("POST run apply: %v", err)
	}
	defer closeBody(resp.Body)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("apply status = %d, want 409", resp.StatusCode)
	}
	assertResponseBodyContains(t, resp, "plan_hash_required")
}

func TestRunCommandAcceptsMatchingPlanHash(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, savedPlanJSONFile), []byte(`{"resource_changes":[]}`), 0o600); err != nil {
		t.Fatalf("write safe plan json: %v", err)
	}
	t.Cleanup(func() { invalidatePlan(projectDir) })
	ref := recordCommandPlan(projectDir, "terraform", "", "", "plan", "safe plan")

	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	resp, err := http.Post(
		srv.URL+"/api/projects/demo/run",
		"application/json",
		strings.NewReader(`{"tool":"terraform","command":"apply","approved":true,"plan_hash":"`+ref.Hash+`"}`),
	)
	if err != nil {
		t.Fatalf("POST run apply: %v", err)
	}
	defer closeBody(resp.Body)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("apply status = %d, want 202", resp.StatusCode)
	}
}

func TestRunCommandRequiresSubmittedPlanHashForAnsiblePlaybook(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	t.Cleanup(func() { invalidatePlan(projectDir) })
	_ = recordCommandPlan(projectDir, "ansible", "", "", "check", "safe check")

	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	resp, err := http.Post(
		srv.URL+"/api/projects/demo/run",
		"application/json",
		strings.NewReader(`{"tool":"ansible","command":"playbook","approved":true}`),
	)
	if err != nil {
		t.Fatalf("POST run playbook: %v", err)
	}
	defer closeBody(resp.Body)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("playbook status = %d, want 409", resp.StatusCode)
	}
	assertResponseBodyContains(t, resp, "plan_hash_required")
}
