package pulumi

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ProjectNameFromDir reads Pulumi.yaml from a project directory. It falls
// back to a sanitized directory name so sync can still write a program into
// hand-created projects that have not been fully initialized yet.
func ProjectNameFromDir(dir string) string {
	var manifest struct {
		Name string `yaml:"name"`
	}
	if data, err := os.ReadFile(filepath.Join(dir, "Pulumi.yaml")); err == nil {
		if err := yaml.Unmarshal(data, &manifest); err == nil && strings.TrimSpace(manifest.Name) != "" {
			return strings.TrimSpace(manifest.Name)
		}
	}
	name := strings.ToLower(filepath.Base(dir))
	name = strings.NewReplacer(" ", "-", ".", "-", "_", "-").Replace(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	name = strings.Trim(b.String(), "-_")
	if name == "" {
		name = "iac-studio"
	} else if name[0] < 'a' || name[0] > 'z' {
		name = fmt.Sprintf("iac-%s", name)
	}
	if len(name) > 100 {
		name = name[:100]
	}
	name = strings.Trim(name, "-_")
	if name == "" {
		name = "iac-studio"
	}
	return name
}
