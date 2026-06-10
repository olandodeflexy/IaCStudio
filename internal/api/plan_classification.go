package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	iacplan "github.com/iac-studio/iac-studio/internal/plan"
)

const (
	savedPlanJSONFile          = "tfplan.json"
	maxPlanClassifyRequestBody = 10 << 20
)

type planClassifyRequest struct {
	PlanJSON json.RawMessage `json:"plan_json,omitempty"`
	PlanText string          `json:"plan_text,omitempty"`
	Tool     string          `json:"tool,omitempty"`
	Env      string          `json:"env,omitempty"`
}

func registerPlanClassificationRoutes(mux *http.ServeMux, projectsDir string) {
	mux.HandleFunc("POST /api/projects/{name}/plan/classify", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxPlanClassifyRequestBody)
		name := r.PathValue("name")
		projectPath, err := safeProjectPath(projectsDir, name)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}

		var req planClassifyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "invalid request body: "+err.Error(), 400)
			return
		}

		tool := effectiveProjectTool(projectPath, req.Tool, req.Env)
		if tool == "multi" {
			http.Error(w, hybridToolResolutionMessage("env is required when classifying plans for hybrid projects", req.Env), 400)
			return
		}
		workDir := projectPath
		if req.Env != "" {
			subPath, subErr := safeSubdir(projectPath, "environments", req.Env)
			if subErr != nil {
				http.Error(w, "invalid env: "+subErr.Error(), 400)
				return
			}
			workDir = subPath
		}

		classification := classifyPlanRequest(workDir, req)
		_ = json.NewEncoder(w).Encode(classification)
	})
}

func classifyPlanRequest(workDir string, req planClassifyRequest) *iacplan.ClassificationResult {
	parser := iacplan.New()
	if len(req.PlanJSON) > 0 {
		result, err := parser.ClassifyFullPlan(string(req.PlanJSON))
		if err != nil {
			return iacplan.UnknownClassification("posted plan JSON could not be parsed")
		}
		return result
	}
	if strings.TrimSpace(req.PlanText) != "" {
		parsed, err := parser.ParseStreamOutput(req.PlanText)
		if err != nil {
			return iacplan.UnknownClassification("posted plan text could not be parsed")
		}
		if len(parsed.Changes) == 0 && strings.Contains(req.PlanText, "to add") {
			return iacplan.UnknownClassification("posted plan text lacks machine-readable resource changes")
		}
		return parser.Classify(parsed)
	}
	return classifySavedPlan(workDir)
}

func classifySavedPlan(workDir string) *iacplan.ClassificationResult {
	data, err := os.ReadFile(filepath.Join(workDir, savedPlanJSONFile))
	if err != nil {
		return iacplan.UnknownClassification("saved plan JSON is unavailable; run plan before applying")
	}
	result, err := iacplan.New().ClassifyFullPlan(string(data))
	if err != nil {
		return iacplan.UnknownClassification("saved plan JSON could not be parsed; rerun plan before applying")
	}
	return result
}

func planSupportsSemanticClassification(tool string) bool {
	return tool == "terraform" || tool == "opentofu"
}

func commandProducesPlan(command string) bool {
	return command == "plan" || command == "preview" || command == "check"
}

func commandRecordsSnapshot(command string) bool {
	return command == "apply" || command == "up" || command == "playbook"
}

func appendPlanClassificationOutput(output string, classification *iacplan.ClassificationResult) string {
	if classification == nil {
		return output
	}
	output = strings.TrimRight(output, "\n")
	if output != "" {
		output += "\n\n"
	}
	output += "--- Semantic Plan Classifier ---\n"
	output += classification.Markdown
	return output + "\n"
}
