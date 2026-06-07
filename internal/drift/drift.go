package drift

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Detector compares Terraform state against code to find drift.
type Detector struct{}

func New() *Detector {
	return &Detector{}
}

const (
	ClassificationLegitimateConfigChange = "legitimate_config_change"
	ClassificationUnauthorizedChange     = "unauthorized_change"
	ClassificationMissingFromState       = "missing_from_state"
	ClassificationUnknown                = "unknown"

	ActionCodifyOrAccept            = "codify_or_accept"
	ActionImportOrApply             = "import_or_apply"
	ActionInvestigate               = "investigate"
	ActionReviewImportOrRemove      = "review_import_or_remove"
	ActionRevertOrCodifyAfterReview = "revert_or_codify_after_review"
)

// DriftReport describes differences between code and deployed state.
type DriftReport struct {
	HasState        bool              `json:"has_state"`
	StatePath       string            `json:"state_path"`
	Drifted         []DriftedResource `json:"drifted"`
	Findings        []DriftFinding    `json:"findings"`
	Missing         []string          `json:"missing"`   // in code but not in state
	Unmanaged       []string          `json:"unmanaged"` // in state but not in code
	InSync          int               `json:"in_sync"`   // resources matching
	Total           int               `json:"total"`
	Classifications map[string]int    `json:"classifications,omitempty"`
	Summary         string            `json:"summary"`
}

// DriftedResource is a resource where state differs from code.
type DriftedResource struct {
	Address           string       `json:"address"`
	Type              string       `json:"type"`
	Name              string       `json:"name"`
	Changes           []DriftField `json:"changes"`
	Status            string       `json:"status"` // drifted | missing | unmanaged
	Classification    string       `json:"classification,omitempty"`
	RecommendedAction string       `json:"recommended_action,omitempty"`
	Reason            string       `json:"reason,omitempty"`
}

// DriftField is a single attribute that drifted.
type DriftField struct {
	Path      string      `json:"path"`
	CodeValue interface{} `json:"code_value"`
	LiveValue interface{} `json:"live_value"`
}

// DriftFinding is a single reviewer-facing drift item. The names use
// expected/current so future cloud reads can reuse the same API shape even
// though the first implementation compares HCL against Terraform state.
type DriftFinding struct {
	Address           string      `json:"address"`
	Type              string      `json:"type"`
	Name              string      `json:"name"`
	Status            string      `json:"status"` // drifted | missing | unmanaged
	Path              string      `json:"path,omitempty"`
	ExpectedValue     interface{} `json:"expected_value,omitempty"`
	CurrentValue      interface{} `json:"current_value,omitempty"`
	Classification    string      `json:"classification"`
	RecommendedAction string      `json:"recommended_action"`
	Reason            string      `json:"reason"`
}

// tfState represents the minimal Terraform state file structure we need.
type tfState struct {
	Version   int               `json:"version"`
	Resources []tfStateResource `json:"resources"`
}

type tfStateResource struct {
	Mode      string            `json:"mode"` // "managed" or "data"
	Type      string            `json:"type"`
	Name      string            `json:"name"`
	Instances []tfStateInstance `json:"instances"`
}

type tfStateInstance struct {
	Attributes map[string]interface{} `json:"attributes"`
}

// Detect compares terraform.tfstate with the parsed code resources.
func (d *Detector) Detect(projectDir string, codeResources map[string]map[string]interface{}) (*DriftReport, error) {
	report := &DriftReport{Findings: []DriftFinding{}, Classifications: map[string]int{}}

	// Find state file
	statePaths := []string{
		filepath.Join(projectDir, "terraform.tfstate"),
		filepath.Join(projectDir, ".terraform", "terraform.tfstate"),
	}

	var stateData []byte
	for _, p := range statePaths {
		if data, err := os.ReadFile(p); err == nil {
			stateData = data
			report.StatePath = p
			break
		}
	}

	if stateData == nil {
		report.HasState = false
		report.Summary = "No terraform.tfstate found. Run 'terraform apply' to create state."
		return report, nil
	}
	report.HasState = true

	// Parse state
	var state tfState
	if err := json.Unmarshal(stateData, &state); err != nil {
		return nil, fmt.Errorf("parsing state file: %w", err)
	}

	// Build state index: address -> attributes
	stateIndex := make(map[string]map[string]interface{})
	for _, res := range state.Resources {
		if res.Mode != "managed" {
			continue
		}
		addr := res.Type + "." + res.Name
		if len(res.Instances) > 0 {
			stateIndex[addr] = res.Instances[0].Attributes
		}
	}

	// Compare code vs state
	for addr, codeProps := range codeResources {
		parts := strings.SplitN(addr, ".", 2)
		if len(parts) != 2 {
			continue
		}
		resType, resName := parts[0], parts[1]

		stateAttrs, inState := stateIndex[addr]
		if !inState {
			report.Missing = append(report.Missing, addr)
			resource := DriftedResource{
				Address: addr, Type: resType, Name: resName, Status: "missing",
				Classification:    ClassificationMissingFromState,
				RecommendedAction: ActionImportOrApply,
				Reason:            "Resource exists in code but is not present in Terraform state.",
			}
			report.Drifted = append(report.Drifted, resource)
			report.addFinding(DriftFinding{
				Address: resource.Address, Type: resource.Type, Name: resource.Name,
				Status: resource.Status, Classification: resource.Classification,
				RecommendedAction: resource.RecommendedAction, Reason: resource.Reason,
			})
			continue
		}

		// Compare attributes
		var changes []DriftField
		for key, codeVal := range codeProps {
			if strings.HasPrefix(key, "__") || key == "tags_all" {
				continue
			}
			if stateVal, ok := stateAttrs[key]; ok {
				if fmt.Sprintf("%v", codeVal) != fmt.Sprintf("%v", stateVal) {
					changes = append(changes, DriftField{
						Path: key, CodeValue: codeVal, LiveValue: stateVal,
					})
				}
			}
		}

		if len(changes) > 0 {
			classification, action, reason := classifyResourceDrift(resType, changes)
			report.Drifted = append(report.Drifted, DriftedResource{
				Address: addr, Type: resType, Name: resName,
				Changes: changes, Status: "drifted",
				Classification: classification, RecommendedAction: action, Reason: reason,
			})
			for _, change := range changes {
				report.addFinding(DriftFinding{
					Address: addr, Type: resType, Name: resName, Status: "drifted",
					Path: change.Path, ExpectedValue: change.CodeValue, CurrentValue: change.LiveValue,
					Classification: classification, RecommendedAction: action, Reason: reason,
				})
			}
		} else {
			report.InSync++
		}
	}

	// Find unmanaged resources (in state but not in code)
	for addr := range stateIndex {
		if _, inCode := codeResources[addr]; !inCode {
			report.Unmanaged = append(report.Unmanaged, addr)
			parts := strings.SplitN(addr, ".", 2)
			resType, resName := "", ""
			if len(parts) == 2 {
				resType, resName = parts[0], parts[1]
			}
			resource := DriftedResource{
				Address: addr, Type: resType, Name: resName, Status: "unmanaged",
				Classification:    ClassificationUnauthorizedChange,
				RecommendedAction: ActionReviewImportOrRemove,
				Reason:            "Resource exists in Terraform state but not in code.",
			}
			report.Drifted = append(report.Drifted, resource)
			report.addFinding(DriftFinding{
				Address: resource.Address, Type: resource.Type, Name: resource.Name,
				Status: resource.Status, Classification: resource.Classification,
				RecommendedAction: resource.RecommendedAction, Reason: resource.Reason,
			})
		}
	}

	report.Total = len(codeResources) + len(report.Unmanaged)
	driftCount := len(report.Drifted)
	report.Summary = fmt.Sprintf("%d resources: %d in sync, %d drifted, %d missing from state, %d unmanaged",
		report.Total, report.InSync, driftCount-len(report.Missing)-len(report.Unmanaged), len(report.Missing), len(report.Unmanaged))

	return report, nil
}

func (r *DriftReport) addFinding(f DriftFinding) {
	r.Findings = append(r.Findings, f)
	r.Classifications[f.Classification]++
}

func classifyResourceDrift(resourceType string, changes []DriftField) (classification, action, reason string) {
	if len(changes) == 0 {
		return ClassificationUnknown, ActionInvestigate, "No field-level drift details were available."
	}
	if isMetadataOnlyDrift(changes) {
		return ClassificationLegitimateConfigChange, ActionCodifyOrAccept, "Only metadata fields drifted."
	}
	switch {
	case isIdentityResource(resourceType):
		return ClassificationUnauthorizedChange, ActionRevertOrCodifyAfterReview, "Identity drift can expand permissions or trust boundaries."
	case isNetworkResource(resourceType):
		return ClassificationUnauthorizedChange, ActionRevertOrCodifyAfterReview, "Network drift can change reachability or public exposure."
	case isStatefulResource(resourceType):
		return ClassificationUnauthorizedChange, ActionRevertOrCodifyAfterReview, "Stateful drift can affect durability, availability, or spend."
	default:
		return ClassificationUnknown, ActionInvestigate, "Drift does not match a known low-risk pattern."
	}
}

func isMetadataOnlyDrift(changes []DriftField) bool {
	for _, change := range changes {
		root := strings.Split(change.Path, ".")[0]
		switch root {
		case "tags", "labels", "annotations", "description":
			continue
		default:
			return false
		}
	}
	return true
}

func isIdentityResource(resourceType string) bool {
	t := strings.ToLower(resourceType)
	return strings.Contains(t, "_iam_") ||
		strings.Contains(t, "iam") ||
		strings.Contains(t, "role_assignment") ||
		strings.Contains(t, "role_binding") ||
		strings.Contains(t, "service_account")
}

func isNetworkResource(resourceType string) bool {
	t := strings.ToLower(resourceType)
	needles := []string{
		"security_group",
		"firewall",
		"network_acl",
		"network_security_group",
		"route",
		"listener",
		"load_balancer",
		"vpc_peering",
		"internet_gateway",
	}
	for _, needle := range needles {
		if strings.Contains(t, needle) {
			return true
		}
	}
	return false
}

func isStatefulResource(resourceType string) bool {
	t := strings.ToLower(resourceType)
	needles := []string{
		"db_instance",
		"db_cluster",
		"rds",
		"dynamodb",
		"s3_bucket",
		"storage_bucket",
		"sql_database",
		"disk",
		"volume",
		"redis",
		"elasticache",
	}
	for _, needle := range needles {
		if strings.Contains(t, needle) {
			return true
		}
	}
	return false
}
