package ai

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// updateGolden regenerates the .golden fixture files instead of asserting
// against them. Run as `go test ./internal/ai/... -update-golden` whenever a
// prompt change is intentional.
var updateGolden = flag.Bool("update-golden", false, "rewrite golden prompt fixtures")

// TestPromptsLoadedSuccessfully guards the package init — if go:embed ever
// drops a prompt file or the frontmatter parser breaks, this fails first.
func TestPromptsLoadedSuccessfully(t *testing.T) {
	want := []string{"system", "provider_aws", "provider_gcp", "provider_azurerm", "provider_ansible"}
	for _, id := range want {
		if _, ok := promptSet[id]; !ok {
			t.Errorf("prompt %q missing from loaded set; loaded: %v", id, keysOf(promptSet))
		}
	}
}

func keysOf(m map[string]*promptEntry) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestSystemPromptGolden locks the rendered system prompt against a checked-in
// fixture for each (tool, provider, canvas?) combination we care about. Any
// intentional edit to the prompt files regenerates the goldens via
// -update-golden so the diff is reviewable.
func TestSystemPromptGolden(t *testing.T) {
	cases := []struct {
		name     string
		tool     string
		provider string
		canvas   []CanvasResource
		golden   string
	}{
		{"aws empty canvas", "terraform", "aws", nil, "system_aws_empty.golden"},
		{"gcp empty canvas", "terraform", "google", nil, "system_gcp_empty.golden"},
		{"azure empty canvas", "terraform", "azurerm", nil, "system_azure_empty.golden"},
		{"ansible empty canvas", "ansible", "", nil, "system_ansible_empty.golden"},
		{"aws with canvas", "terraform", "aws", []CanvasResource{
			{Type: "aws_vpc", Name: "main"},
			{Type: "aws_subnet", Name: "public_1"},
		}, "system_aws_with_canvas.golden"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildSystemPrompt(tc.tool, tc.provider, tc.canvas)
			path := filepath.Join("testdata", "golden", tc.golden)
			if *updateGolden {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %s: %v — regenerate with -update-golden", path, err)
			}
			if string(want) != got {
				// Show a compact first-diff hint rather than dumping both full strings.
				t.Errorf("system prompt drift (golden %s)\nfirst diff at byte %d\n--- want ---\n%s\n--- got ---\n%s",
					path, firstDiff(string(want), got), truncate(string(want), 400), truncate(got, 400))
			}
		})
	}
}

// TestProviderGuideGolden locks each provider-guide template in isolation
// so a change to the AWS guide doesn't silently alter the Azure golden.
func TestProviderGuideGolden(t *testing.T) {
	cases := []struct {
		id     string
		golden string
	}{
		{"provider_aws", "provider_aws.golden"},
		{"provider_gcp", "provider_gcp.golden"},
		{"provider_azurerm", "provider_azurerm.golden"},
		{"provider_ansible", "provider_ansible.golden"},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			got := renderPrompt(tc.id, nil)
			path := filepath.Join("testdata", "golden", tc.golden)
			if *updateGolden {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}
			if string(want) != got {
				t.Errorf("%s drift\n--- want ---\n%s\n--- got ---\n%s", tc.id, want, got)
			}
		})
	}
}

func firstDiff(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…" + "\n(truncated, full length " + itoa(len(s)) + ")"
}

func itoa(n int) string {
	return strings.TrimLeft(string([]byte{
		byte('0' + n/1000000%10),
		byte('0' + n/100000%10),
		byte('0' + n/10000%10),
		byte('0' + n/1000%10),
		byte('0' + n/100%10),
		byte('0' + n/10%10),
		byte('0' + n%10),
	}), "0")
}
