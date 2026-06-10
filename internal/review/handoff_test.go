package review

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCreatePullRequestHandoffCommitsOnlyGeneratedArtifacts(t *testing.T) {
	projectPath := newGitProject(t)
	files := writeReviewArtifacts(t, projectPath, ".iac-studio/remediations/iac-studio-drift-revert-demo", "pr-body.md")

	handoff, err := CreatePullRequestHandoff(PullRequestHandoffInput{
		ProjectPath:   projectPath,
		Title:         "Revert unauthorized drift for demo",
		Branch:        "iac-studio-drift-revert-demo",
		CommitMessage: "Document drift revert for demo",
		BodyPath:      ".iac-studio/remediations/iac-studio-drift-revert-demo/pr-body.md",
		Files:         files,
	})
	if err != nil {
		t.Fatalf("create handoff: %v", err)
	}
	if handoff.Branch != "iac-studio-drift-revert-demo" || handoff.BaseBranch != "main" || handoff.Commit == "" {
		t.Fatalf("unexpected handoff metadata: %#v", handoff)
	}
	if len(handoff.Commands) != 2 ||
		!strings.Contains(handoff.Commands[0].Display, "git push -u origin iac-studio-drift-revert-demo") ||
		!strings.Contains(handoff.Commands[1].Display, "gh pr create") {
		t.Fatalf("unexpected handoff commands: %#v", handoff.Commands)
	}

	status := gitOut(t, projectPath, "status", "--short")
	if status != "" {
		t.Fatalf("worktree should be clean after review commit, got:\n%s", status)
	}
	committed := gitOut(t, projectPath, "show", "--name-only", "--format=", "HEAD")
	for _, file := range files {
		if !strings.Contains(committed, file) {
			t.Fatalf("review commit missing %s:\n%s", file, committed)
		}
	}
}

func TestCreatePullRequestHandoffRejectsUnrelatedDirtyFiles(t *testing.T) {
	projectPath := newGitProject(t)
	files := writeReviewArtifacts(t, projectPath, ".iac-studio/remediations/iac-studio-drift-revert-demo", "pr-body.md")
	if err := os.WriteFile(filepath.Join(projectPath, "main.tf"), []byte("resource drift\n"), 0o644); err != nil {
		t.Fatalf("write unrelated dirty file: %v", err)
	}

	_, err := CreatePullRequestHandoff(PullRequestHandoffInput{
		ProjectPath:   projectPath,
		Title:         "Revert unauthorized drift for demo",
		Branch:        "iac-studio-drift-revert-demo",
		CommitMessage: "Document drift revert for demo",
		BodyPath:      ".iac-studio/remediations/iac-studio-drift-revert-demo/pr-body.md",
		Files:         files,
	})
	if !errors.Is(err, ErrDirtyWorktree) {
		t.Fatalf("error = %v, want ErrDirtyWorktree", err)
	}
	if branch := gitOut(t, projectPath, "rev-parse", "--abbrev-ref", "HEAD"); branch != "main" {
		t.Fatalf("dirty worktree rejection should leave branch on main, got %q", branch)
	}
}

func TestCreatePullRequestHandoffRejectsNonArtifactFiles(t *testing.T) {
	projectPath := newGitProject(t)
	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("not generated\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	_, err := CreatePullRequestHandoff(PullRequestHandoffInput{
		ProjectPath:   projectPath,
		Title:         "Unsafe PR",
		Branch:        "iac-studio-unsafe",
		CommitMessage: "Unsafe commit",
		BodyPath:      "README.md",
		Files:         []string{"README.md"},
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}

func TestCreatePullRequestHandoffRejectsSymlinkArtifacts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}
	projectPath := newGitProject(t)
	root := filepath.Join(projectPath, ".iac-studio", "remediations", "iac-studio-drift-revert-demo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir artifact root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "pr-body.md"), []byte("body\n"), 0o644); err != nil {
		t.Fatalf("write body: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "proposal.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	if err := os.Symlink(filepath.Join(projectPath, "README.md"), filepath.Join(root, "README.md")); err != nil {
		t.Fatalf("symlink artifact: %v", err)
	}

	_, err := CreatePullRequestHandoff(PullRequestHandoffInput{
		ProjectPath:   projectPath,
		Title:         "Revert unauthorized drift for demo",
		Branch:        "iac-studio-drift-revert-demo",
		CommitMessage: "Document drift revert for demo",
		BodyPath:      ".iac-studio/remediations/iac-studio-drift-revert-demo/pr-body.md",
		Files: []string{
			".iac-studio/remediations/iac-studio-drift-revert-demo/README.md",
			".iac-studio/remediations/iac-studio-drift-revert-demo/pr-body.md",
			".iac-studio/remediations/iac-studio-drift-revert-demo/proposal.json",
		},
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}

func newGitProject(t *testing.T) string {
	t.Helper()
	projectPath := t.TempDir()
	git(t, projectPath, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("# demo\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	git(t, projectPath, "add", "README.md")
	git(t, projectPath, "commit", "-m", "Initial commit")
	return projectPath
}

func writeReviewArtifacts(t *testing.T, projectPath, root, bodyFile string) []string {
	t.Helper()
	artifactDir := filepath.Join(projectPath, filepath.FromSlash(root))
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatalf("mkdir artifact dir: %v", err)
	}
	files := []string{
		root + "/README.md",
		root + "/" + bodyFile,
		root + "/proposal.json",
	}
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(projectPath, filepath.FromSlash(file)), []byte(file+"\n"), 0o644); err != nil {
			t.Fatalf("write artifact %s: %v", file, err)
		}
	}
	return files
}

func git(t *testing.T, dir string, args ...string) {
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

func gitOut(t *testing.T, dir string, args ...string) string {
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
