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

// DriftReport describes differences between code and deployed state.
type DriftReport struct {
	HasState    bool           `json:"has_state"`
	StatePath   string         `json:"state_path"`
	Drifted     []DriftedResource `json:"drifted"`
	Missing     []string       `json:"missing"`       // in code but not in state
	Unmanaged   []string       `json:"unmanaged"`     // in state but not in code
	InSync      int            `json:"in_sync"`       // resources matching
	Total       int            `json:"total"`
	Summary     string         `json:"summary"`
}

// DriftedResource is a resource where state differs from code.
type DriftedResource struct {
	Address  string       `json:"address"`
	Type     string       `json:"type"`
	Name     string       `json:"name"`
	Changes  []DriftField `json:"changes"`
	Status   string       `json:"status"` // drifted | missing | unmanaged
}

// DriftField is a single attribute that drifted.
type DriftField struct {
	Path      string      `json:"path"`
	CodeValue interface{} `json:"code_value"`
	LiveValue interface{} `json:"live_value"`
}

// tfState represents the minimal Terraform state file structure we need.
type tfState struct {
	Version   int `json:"version"`
	Resources []tfStateResource `json:"resources"`
}

type tfStateResource struct {
	Mode      string `json:"mode"` // "managed" or "data"
	Type      string `json:"type"`
	Name      string `json:"name"`
	Instances []tfStateInstance `json:"instances"`
}

type tfStateInstance struct {
	Attributes map[string]interface{} `json:"attributes"`
}

// Detect compares terraform.tfstate with the parsed code resources.
func (d *Detector) Detect(projectDir string, codeResources map[string]map[string]interface{}) (*DriftReport, error) {
	report := &DriftReport{}

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
			report.Drifted = append(report.Drifted, DriftedResource{
				Address: addr, Type: resType, Name: resName, Status: "missing",
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
			report.Drifted = append(report.Drifted, DriftedResource{
				Address: addr, Type: resType, Name: resName,
				Changes: changes, Status: "drifted",
			})
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
			report.Drifted = append(report.Drifted, DriftedResource{
				Address: addr, Type: resType, Name: resName, Status: "unmanaged",
			})
		}
	}

	report.Total = len(codeResources) + len(report.Unmanaged)
	driftCount := len(report.Drifted)
	report.Summary = fmt.Sprintf("%d resources: %d in sync, %d drifted, %d missing from state, %d unmanaged",
		report.Total, report.InSync, driftCount-len(report.Missing)-len(report.Unmanaged), len(report.Missing), len(report.Unmanaged))

	return report, nil
}
