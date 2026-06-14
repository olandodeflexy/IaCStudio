package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	planGateTTL            = time.Hour
	savedTerraformPlanFile = "tfplan"
)

type planReference struct {
	Hash         string `json:"hash"`
	Tool         string `json:"tool"`
	Env          string `json:"env,omitempty"`
	ConnectionID string `json:"connection_id,omitempty"`
	PlanCommand  string `json:"plan_command"`
	CreatedAt    string `json:"created_at"`
	ExpiresAt    string `json:"expires_at"`
}

type planRecord struct {
	reference planReference
	createdAt time.Time
}

type planGateRejection struct {
	Error  string
	Detail string
}

// planGate tracks the exact successful plan/preview/check artifact that must
// be acknowledged before a mutating command can run.
var planGate = struct {
	mu    sync.Mutex
	plans map[string]planRecord // projectPath -> latest plan record
}{plans: make(map[string]planRecord)}

func invalidatePlan(projectPaths ...string) {
	planGate.mu.Lock()
	defer planGate.mu.Unlock()
	for _, projectPath := range projectPaths {
		delete(planGate.plans, projectPath)
	}
}

func invalidatePlanForChangedFile(projectsDir, file string) {
	absProjects, err := filepath.Abs(projectsDir)
	if err != nil {
		return
	}
	absFile, err := filepath.Abs(file)
	if err != nil {
		return
	}
	rel, err := filepath.Rel(absProjects, absFile)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 || parts[0] == "" {
		return
	}
	projectPath := filepath.Join(absProjects, parts[0])
	invalidatePlan(planInvalidationPaths(projectPath, absFile)...)
}

func recordPlan(projectPath string) string {
	return recordCommandPlan(projectPath, "terraform", "", "", "plan", "").Hash
}

func recordCommandPlan(projectPath, tool, env, connectionID, command, output string) planReference {
	return recordCommandPlanAt(projectPath, tool, env, connectionID, command, output, time.Now().UTC())
}

func recordCommandPlanAt(projectPath, tool, env, connectionID, command, output string, now time.Time) planReference {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	ref := planReference{
		Hash:         planHash(projectPath, tool, env, connectionID, command, output),
		Tool:         tool,
		Env:          env,
		ConnectionID: connectionID,
		PlanCommand:  command,
		CreatedAt:    now.Format(time.RFC3339),
		ExpiresAt:    now.Add(planGateTTL).Format(time.RFC3339),
	}

	planGate.mu.Lock()
	planGate.plans[projectPath] = planRecord{reference: ref, createdAt: now}
	planGate.mu.Unlock()
	return ref
}

func hasPlan(projectPath string) bool {
	planGate.mu.Lock()
	defer planGate.mu.Unlock()
	record, ok := planGate.plans[projectPath]
	return ok && time.Since(record.createdAt) < planGateTTL
}

func validatePlanForCommand(projectPath, tool, env, connectionID, command, providedHash string) (*planReference, *planGateRejection) {
	planGate.mu.Lock()
	record, ok := planGate.plans[projectPath]
	planGate.mu.Unlock()
	if !ok || time.Since(record.createdAt) >= planGateTTL {
		return nil, &planGateRejection{
			Error:  "plan_required",
			Detail: "run plan first; no current saved plan exists for this project, environment, and connection",
		}
	}

	ref := record.reference
	if ref.Tool != tool || ref.Env != env || ref.ConnectionID != connectionID || !planCommandAllowsMutation(tool, ref.PlanCommand, command) {
		return &ref, &planGateRejection{
			Error:  "plan_hash_mismatch",
			Detail: "saved plan does not match the requested tool, environment, connection, or command; rerun plan",
		}
	}

	if strings.TrimSpace(providedHash) == "" {
		return &ref, &planGateRejection{
			Error:  "plan_hash_required",
			Detail: "saved plan exists, but apply requires the exact plan_hash returned by the latest plan run",
		}
	}
	if strings.TrimSpace(providedHash) != ref.Hash {
		return &ref, &planGateRejection{
			Error:  "plan_hash_mismatch",
			Detail: "submitted plan_hash does not match the latest saved plan; rerun plan",
		}
	}
	return &ref, nil
}

func planCommandAllowsMutation(tool, planCommand, mutationCommand string) bool {
	switch tool {
	case "ansible":
		return planCommand == "check" && (mutationCommand == "apply" || mutationCommand == "playbook")
	case "pulumi":
		return (planCommand == "plan" || planCommand == "preview") &&
			(mutationCommand == "apply" || mutationCommand == "up" || mutationCommand == "destroy" || mutationCommand == "refresh")
	case "terraform", "opentofu":
		return planCommand == "plan" && (mutationCommand == "apply" || mutationCommand == "destroy")
	default:
		return commandProducesPlan(planCommand)
	}
}

func appendPlanReferenceOutput(output string, ref planReference) string {
	output = strings.TrimRight(output, "\n")
	if output != "" {
		output += "\n\n"
	}
	return output + fmt.Sprintf("--- Plan Gate ---\nPlan hash: %s\nExpires: %s\n", ref.Hash, ref.ExpiresAt)
}

func planHash(projectPath, tool, env, connectionID, command, output string) string {
	h := sha256.New()
	writeHashField(h, "iac-studio-plan-gate-v1")
	writeHashField(h, tool)
	writeHashField(h, env)
	writeHashField(h, connectionID)
	writeHashField(h, command)
	writeHashField(h, output)
	for _, name := range []string{savedTerraformPlanFile, savedPlanJSONFile} {
		data, err := os.ReadFile(filepath.Join(projectPath, name))
		if err != nil {
			continue
		}
		writeHashField(h, name)
		_, _ = h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func writeHashField(w io.Writer, value string) {
	_, _ = io.WriteString(w, value)
	_, _ = w.Write([]byte{0})
}
