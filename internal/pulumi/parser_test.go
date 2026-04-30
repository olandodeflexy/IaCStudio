package pulumi

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/iac-studio/iac-studio/internal/parser"
)

func TestTSParser_ParseGeneratedProgram(t *testing.T) {
	program := RenderProgram(ProjectConfig{Name: "acme", Resources: sampleResources()})
	resources, err := ParseProgram("index.ts", []byte(program))
	if err != nil {
		t.Fatalf("ParseProgram: %v", err)
	}
	if len(resources) != 2 {
		t.Fatalf("want 2 resources, got %d: %+v", len(resources), resources)
	}
	if resources[0].Type != "aws_s3_bucket" || resources[0].Name != "logs" {
		t.Fatalf("first resource = %s.%s, want aws_s3_bucket.logs", resources[0].Type, resources[0].Name)
	}
	if resources[1].Type != "aws_vpc" || resources[1].Properties["cidr_block"] != "10.0.0.0/16" {
		t.Fatalf("second resource did not round-trip VPC properties: %+v", resources[1])
	}
}

func TestSyncProgram_PreservesNonResourceCode(t *testing.T) {
	existing := `import * as pulumi from "@pulumi/pulumi";
import * as aws from "@pulumi/aws";

const config = new pulumi.Config("acme");
const environment = config.get("environment") ?? "dev";

// User helper must survive canvas sync.
function keepMe() {
    return environment;
}

const main = new aws.ec2.Vpc("main", {
    cidrBlock: "10.0.0.0/16",
});
`

	code, err := SyncProgram(existing, ProjectConfig{
		Name: "acme",
		Resources: []parser.Resource{
			{ID: "aws_vpc.main", Type: "aws_vpc", Name: "main", Properties: map[string]any{"cidr_block": "10.0.0.0/16"}},
			{ID: "aws_s3_bucket.logs", Type: "aws_s3_bucket", Name: "logs", Properties: map[string]any{"bucket": "acme-logs"}},
		},
	})
	if err != nil {
		t.Fatalf("SyncProgram: %v", err)
	}
	mustContain(t, code, "function keepMe()")
	mustContain(t, code, managedStartMarker)
	mustContain(t, code, `new aws.s3.Bucket("logs"`)

	parsed, err := ParseProgram("index.ts", []byte(code))
	if err != nil {
		t.Fatalf("Parse synced program: %v", err)
	}
	if len(parsed) != 2 {
		t.Fatalf("want 2 parsed resources after sync, got %d", len(parsed))
	}
}

func TestTSParser_ParseDirOnlyReadsTopLevelEntrypoint(t *testing.T) {
	dir := t.TempDir()
	rootProgram := RenderProgram(ProjectConfig{
		Name: "acme",
		Resources: []parser.Resource{{
			ID:         "aws_vpc.main",
			Type:       "aws_vpc",
			Name:       "main",
			Properties: map[string]any{"cidr_block": "10.0.0.0/16"},
		}},
	})
	if err := os.WriteFile(filepath.Join(dir, "index.ts"), []byte(rootProgram), 0o600); err != nil {
		t.Fatalf("write root index.ts: %v", err)
	}
	nestedDir := filepath.Join(dir, "node_modules", "pkg")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("mkdir nested node_modules: %v", err)
	}
	nestedProgram := RenderProgram(ProjectConfig{
		Name: "nested",
		Resources: []parser.Resource{{
			ID:         "aws_s3_bucket.logs",
			Type:       "aws_s3_bucket",
			Name:       "logs",
			Properties: map[string]any{"bucket": "ignored"},
		}},
	})
	if err := os.WriteFile(filepath.Join(nestedDir, "index.ts"), []byte(nestedProgram), 0o600); err != nil {
		t.Fatalf("write nested index.ts: %v", err)
	}

	resources, err := (&TSParser{}).ParseDir(dir)
	if err != nil {
		t.Fatalf("ParseDir: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("want only top-level resource, got %d: %+v", len(resources), resources)
	}
	if resources[0].Type != "aws_vpc" || resources[0].Name != "main" {
		t.Fatalf("ParseDir read wrong resource: %+v", resources[0])
	}
	if resources[0].File != filepath.Join(dir, "index.ts") {
		t.Fatalf("resource file = %q, want top-level index.ts", resources[0].File)
	}
}

func TestParseTSStringLiteral_SingleQuoteEscapes(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{src: `'\n'`, want: "\n"},
		{src: `'\\n'`, want: `\n`},
		{src: `'it\'s'`, want: "it's"},
	}
	for _, tc := range cases {
		got, ok := parseTSStringLiteral(tc.src)
		if !ok {
			t.Fatalf("parseTSStringLiteral(%q) did not parse", tc.src)
		}
		if got != tc.want {
			t.Errorf("parseTSStringLiteral(%q) = %q, want %q", tc.src, got, tc.want)
		}
	}
}

func TestSyncProgram_DedupesEquivalentImports(t *testing.T) {
	existing := `import * as pulumi from '@pulumi/pulumi';
import  *  as  aws from '@pulumi/aws';

const config = new pulumi.Config("acme");
`

	code, err := SyncProgram(existing, ProjectConfig{
		Name: "acme",
		Resources: []parser.Resource{{
			ID:         "aws_vpc.main",
			Type:       "aws_vpc",
			Name:       "main",
			Properties: map[string]any{"cidr_block": "10.0.0.0/16"},
		}},
	})
	if err != nil {
		t.Fatalf("SyncProgram: %v", err)
	}
	if strings.Count(code, "@pulumi/aws") != 1 {
		t.Fatalf("expected one aws import after sync, got:\n%s", code)
	}
	if strings.Count(code, "@pulumi/pulumi") != 1 {
		t.Fatalf("expected one pulumi import after sync, got:\n%s", code)
	}
}

func TestSyncProgram_RoundTripParseSerializeParseIsStable(t *testing.T) {
	first, err := SyncProgram("", ProjectConfig{Name: "acme", Resources: sampleResources()})
	if err != nil {
		t.Fatalf("first SyncProgram: %v", err)
	}
	parsedFirst, err := ParseProgram("index.ts", []byte(first))
	if err != nil {
		t.Fatalf("parse first: %v", err)
	}
	second, err := SyncProgram(first, ProjectConfig{Name: "acme", Resources: parsedFirst})
	if err != nil {
		t.Fatalf("second SyncProgram: %v", err)
	}
	parsedSecond, err := ParseProgram("index.ts", []byte(second))
	if err != nil {
		t.Fatalf("parse second: %v", err)
	}
	clearSourceLocations(parsedFirst)
	clearSourceLocations(parsedSecond)
	if !reflect.DeepEqual(parsedFirst, parsedSecond) {
		t.Fatalf("parse -> sync -> parse drifted:\nfirst=%+v\nsecond=%+v\ncode=%s", parsedFirst, parsedSecond, second)
	}
	if strings.Count(second, managedStartMarker) != 1 {
		t.Fatalf("managed marker should appear once:\n%s", second)
	}
	if strings.Count(second, "// Exports") != 1 {
		t.Fatalf("exports section should appear once:\n%s", second)
	}
}

func clearSourceLocations(resources []parser.Resource) {
	for i := range resources {
		resources[i].File = ""
		resources[i].Line = 0
	}
}
