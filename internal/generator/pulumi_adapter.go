package generator

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/iac-studio/iac-studio/internal/parser"
	pulumigen "github.com/iac-studio/iac-studio/internal/pulumi"
)

// PulumiGenerator adapts the Pulumi project renderer to the generic generator
// interface used by legacy create/sync call sites.
type PulumiGenerator struct{}

func (g *PulumiGenerator) FileExtension() string { return ".ts" }

func (g *PulumiGenerator) Generate(resources []parser.Resource) (string, error) {
	return pulumigen.RenderProgram(pulumigen.ProjectConfig{
		Name:      "iac-studio",
		Resources: resources,
	}), nil
}

func (g *PulumiGenerator) WriteScaffold(dir string) error {
	name := pulumigen.ProjectNameFromDir(dir)
	files, err := pulumigen.GenerateProject(pulumigen.ProjectConfig{
		Name:         name,
		Environments: []string{"dev"},
	})
	if err != nil {
		return err
	}
	for _, file := range files {
		path := filepath.Join(dir, file.Path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, file.Content, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", file.Path, err)
		}
	}
	return nil
}
