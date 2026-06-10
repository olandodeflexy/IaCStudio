package api

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initProjectGitRepo(t *testing.T, projectPath string) {
	t.Helper()
	gitForTest(t, projectPath, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("# demo\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	gitForTest(t, projectPath, "add", "-A")
	gitForTest(t, projectPath, "commit", "-m", "Initial commit")
}

func gitForTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=IaC Studio",
		"GIT_AUTHOR_EMAIL=iac-studio@local",
		"GIT_COMMITTER_NAME=IaC Studio",
		"GIT_COMMITTER_EMAIL=iac-studio@local",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %s: %v", strings.Join(args, " "), string(out), err)
	}
}

func gitOutputForTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=IaC Studio",
		"GIT_AUTHOR_EMAIL=iac-studio@local",
		"GIT_COMMITTER_NAME=IaC Studio",
		"GIT_COMMITTER_EMAIL=iac-studio@local",
	)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s failed: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}
