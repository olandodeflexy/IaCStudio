package importer

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/iac-studio/iac-studio/internal/catalog"
	"github.com/iac-studio/iac-studio/internal/parser"
)

// FileEntry represents a file or directory for the browser.
type FileEntry struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	IsDir    bool   `json:"is_dir"`
	Size     int64  `json:"size"`
	Ext      string `json:"ext,omitempty"`
	Children int    `json:"children,omitempty"` // number of child entries if directory
}

// BrowseDir lists the contents of a directory for the file browser.
// Restricts browsing to the user's home directory and below for safety.
func BrowseDir(dir string) ([]FileEntry, error) {
	// Resolve symlinks and normalize
	resolved, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	// Safety: restrict to home directory
	home, _ := os.UserHomeDir()
	if !strings.HasPrefix(resolved, home) && resolved != "/" && resolved != home {
		return nil, fmt.Errorf("browsing restricted to home directory")
	}

	entries, err := os.ReadDir(resolved)
	if err != nil {
		return nil, fmt.Errorf("cannot read directory: %w", err)
	}

	var result []FileEntry
	for _, e := range entries {
		// Skip hidden files/dirs except .terraform
		if strings.HasPrefix(e.Name(), ".") && e.Name() != ".terraform" {
			continue
		}
		// Skip common noise
		if e.Name() == "node_modules" || e.Name() == "__pycache__" || e.Name() == ".git" {
			continue
		}

		entry := FileEntry{
			Name:  e.Name(),
			Path:  filepath.Join(resolved, e.Name()),
			IsDir: e.IsDir(),
			Ext:   filepath.Ext(e.Name()),
		}

		if e.IsDir() {
			// Count children for directory badges
			if sub, err := os.ReadDir(entry.Path); err == nil {
				entry.Children = len(sub)
			}
		} else {
			if info, err := e.Info(); err == nil {
				entry.Size = info.Size()
			}
		}

		result = append(result, entry)
	}

	// Sort: directories first, then alphabetical
	sort.Slice(result, func(i, j int) bool {
		if result[i].IsDir != result[j].IsDir {
			return result[i].IsDir
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})

	return result, nil
}

// DetectedProject describes what was found when scanning a directory.
type DetectedProject struct {
	Tool       string            `json:"tool"`        // terraform | opentofu | ansible | unknown
	Provider   string            `json:"provider"`    // aws | google | azurerm | multi
	Files      []ProjectFile     `json:"files"`       // relevant files found
	Resources  []parser.Resource `json:"resources"`   // parsed resources
	Edges      []DetectedEdge    `json:"edges"`       // inferred connections
	Summary    string            `json:"summary"`     // human-readable description
	Warnings   []string          `json:"warnings,omitempty"`
}

// ProjectFile is a file found during import scanning.
type ProjectFile struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	Type     string `json:"type"` // terraform | ansible | vars | output | config | other
	Size     int64  `json:"size"`
}

// DetectedEdge is a connection inferred from parsed resource references.
type DetectedEdge struct {
	FromID string `json:"from_id"`
	ToID   string `json:"to_id"`
	Field  string `json:"field"`
}

// ScanProject scans a directory, detects the IaC tool, parses all files,
// and reconstructs the topology by inferring connections from references.
func ScanProject(dir string) (*DetectedProject, error) {
	resolved, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("path does not exist: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("path is not a directory")
	}

	project := &DetectedProject{}

	// Scan for files
	var tfFiles, ymlFiles []string
	err = filepath.Walk(resolved, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			// Skip .terraform, .git, node_modules
			if info != nil && info.IsDir() {
				base := filepath.Base(path)
				if base == ".terraform" || base == ".git" || base == "node_modules" {
					return filepath.SkipDir
				}
			}
			return err
		}

		rel, _ := filepath.Rel(resolved, path)
		ext := filepath.Ext(path)
		pf := ProjectFile{
			Path: path,
			Name: rel,
			Size: info.Size(),
		}

		switch ext {
		case ".tf":
			pf.Type = "terraform"
			tfFiles = append(tfFiles, path)
		case ".tfvars":
			pf.Type = "vars"
		case ".yml", ".yaml":
			pf.Type = "ansible"
			ymlFiles = append(ymlFiles, path)
		case ".hcl":
			pf.Type = "config"
		default:
			pf.Type = "other"
		}
		project.Files = append(project.Files, pf)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scanning directory: %w", err)
	}

	// Detect tool
	if len(tfFiles) > 0 {
		// Check if OpenTofu by looking for .terraform.lock.hcl with opentofu
		lockFile := filepath.Join(resolved, ".terraform.lock.hcl")
		if data, err := os.ReadFile(lockFile); err == nil && strings.Contains(string(data), "opentofu") {
			project.Tool = "opentofu"
		} else {
			project.Tool = "terraform"
		}

		// Parse all .tf files
		p := &parser.HCLParser{}
		for _, f := range tfFiles {
			resources, err := p.ParseFile(f)
			if err != nil {
				project.Warnings = append(project.Warnings, fmt.Sprintf("parse error in %s: %v", filepath.Base(f), err))
				continue
			}
			project.Resources = append(project.Resources, resources...)
		}
	} else if len(ymlFiles) > 0 {
		project.Tool = "ansible"
		p := &parser.YAMLParser{}
		for _, f := range ymlFiles {
			resources, err := p.ParseFile(f)
			if err != nil {
				project.Warnings = append(project.Warnings, fmt.Sprintf("parse error in %s: %v", filepath.Base(f), err))
				continue
			}
			project.Resources = append(project.Resources, resources...)
		}
	} else {
		project.Tool = "unknown"
		project.Summary = "No Terraform (.tf) or Ansible (.yml) files found in this directory."
		return project, nil
	}

	// Detect provider from resource types
	project.Provider = detectProvider(project.Resources)

	// Infer connections from resource references
	project.Edges = inferConnections(project.Resources, project.Tool)

	// Generate summary
	project.Summary = fmt.Sprintf("Found %d resources across %d files (%s, %s provider)",
		len(project.Resources), countFilesByType(project.Files, project.Tool), project.Tool, project.Provider)

	return project, nil
}

func detectProvider(resources []parser.Resource) string {
	counts := map[string]int{"aws": 0, "google": 0, "azurerm": 0}
	for _, r := range resources {
		switch {
		case strings.HasPrefix(r.Type, "aws_"):
			counts["aws"]++
		case strings.HasPrefix(r.Type, "google_"):
			counts["google"]++
		case strings.HasPrefix(r.Type, "azurerm_"):
			counts["azurerm"]++
		}
	}
	total := counts["aws"] + counts["google"] + counts["azurerm"]
	if total == 0 {
		return "unknown"
	}
	// Check if multi-provider
	nonZero := 0
	for _, c := range counts {
		if c > 0 {
			nonZero++
		}
	}
	if nonZero > 1 {
		return "multi"
	}
	if counts["google"] > 0 {
		return "google"
	}
	if counts["azurerm"] > 0 {
		return "azurerm"
	}
	return "aws"
}

func inferConnections(resources []parser.Resource, tool string) []DetectedEdge {
	if tool == "ansible" {
		return nil // Ansible tasks don't have structural references
	}

	var edges []DetectedEdge

	// Build catalog connection rules
	cat := catalog.GetCatalog(tool)
	connectsVia := make(map[string]map[string]string)
	for _, cr := range cat.Resources {
		if len(cr.ConnectsVia) > 0 {
			connectsVia[cr.Type] = cr.ConnectsVia
		}
	}

	// Build type -> resource index
	byType := make(map[string][]parser.Resource)
	for _, r := range resources {
		byType[r.Type] = append(byType[r.Type], r)
	}

	// Method 1: Catalog-based connection inference
	for _, r := range resources {
		if rules, ok := connectsVia[r.Type]; ok {
			for field, targetType := range rules {
				targets := byType[targetType]
				if len(targets) == 0 {
					continue
				}

				// Check if the resource has an explicit reference in its properties
				if propVal, ok := r.Properties[field]; ok {
					propStr := fmt.Sprintf("%v", propVal)
					// Try to match against existing resource names
					for _, target := range targets {
						ref := fmt.Sprintf("%s.%s.", target.Type, target.Name)
						if strings.Contains(propStr, ref) || strings.Contains(propStr, target.Name) {
							edges = append(edges, DetectedEdge{
								FromID: r.ID,
								ToID:   target.ID,
								Field:  field,
							})
							break
						}
					}
				} else {
					// No explicit reference — connect to first instance of target type
					edges = append(edges, DetectedEdge{
						FromID: r.ID,
						ToID:   targets[0].ID,
						Field:  field,
					})
				}
			}
		}
	}

	// Method 2: Property value pattern matching for references not in catalog
	for _, r := range resources {
		for field, val := range r.Properties {
			valStr := fmt.Sprintf("%v", val)
			// Look for terraform reference patterns: type.name.attribute
			for _, target := range resources {
				if target.ID == r.ID {
					continue
				}
				ref := target.Type + "." + target.Name + "."
				if strings.Contains(valStr, ref) {
					// Check if we already have this edge
					duplicate := false
					for _, e := range edges {
						if e.FromID == r.ID && e.ToID == target.ID {
							duplicate = true
							break
						}
					}
					if !duplicate {
						edges = append(edges, DetectedEdge{
							FromID: r.ID,
							ToID:   target.ID,
							Field:  field,
						})
					}
				}
			}
		}
	}

	return edges
}

func countFilesByType(files []ProjectFile, tool string) int {
	count := 0
	for _, f := range files {
		if f.Type == tool {
			count++
		}
	}
	return count
}
