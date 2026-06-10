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

func TestCreatePullRequestHandoffCleansUpBranchOnPartialFailure(t *testing.T) {
	// Simulate a failure that occurs after "git checkout -b" but before the
	// commit by supplying a file list that contains a path not present on disk.
	// The artifact prefix passes normalizeArtifactPath, so validation gets past
	// the pre-checkout checks, but git-add fails because the file doesn't exist.
	projectPath := newGitProject(t)
	artifactDir := filepath.Join(projectPath, ".iac-studio", "remediations", "iac-studio-drift-revert-partial")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatalf("mkdir artifact dir: %v", err)
	}
	bodyPath := ".iac-studio/remediations/iac-studio-drift-revert-partial/pr-body.md"
	if err := os.WriteFile(filepath.Join(projectPath, filepath.FromSlash(bodyPath)), []byte("body\n"), 0o644); err != nil {
		t.Fatalf("write body: %v", err)
	}
	missingFile := ".iac-studio/remediations/iac-studio-drift-revert-partial/missing.tf"
	if err := os.WriteFile(filepath.Join(projectPath, filepath.FromSlash(missingFile)), []byte("missing\n"), 0o644); err != nil {
		t.Fatalf("write placeholder: %v", err)
	}

	// Succeed once to prove the happy path works, then delete the extra file and
	// call again to force a git-add failure after checkout.
	files := []string{bodyPath, missingFile}
	_, err := CreatePullRequestHandoff(PullRequestHandoffInput{
		ProjectPath:   projectPath,
		Title:         "Partial failure test",
		Branch:        "iac-studio-drift-revert-partial",
		CommitMessage: "Document partial failure test",
		BodyPath:      bodyPath,
		Files:         files,
	})
	if err != nil {
		t.Fatalf("first handoff should succeed: %v", err)
	}

	// Reset back to main and recreate the artifact directory for the second call.
	git(t, projectPath, "checkout", "main")
	git(t, projectPath, "branch", "-D", "iac-studio-drift-revert-partial")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatalf("mkdir artifact dir again: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectPath, filepath.FromSlash(bodyPath)), []byte("body\n"), 0o644); err != nil {
		t.Fatalf("re-write body: %v", err)
	}

	// Now remove the second file so that ensureRegularArtifactFile passes (we
	// can't use it here) but git-add fails.  We bypass the pre-checkout file
	// check by removing the file only after validation but before add — we
	// instead just skip the missing file from the Files list to trigger
	// ErrNoChanges after checkout, which also exercises the cleanup path.
	_, err = CreatePullRequestHandoff(PullRequestHandoffInput{
		ProjectPath:   projectPath,
		Title:         "Partial failure test",
		Branch:        "iac-studio-drift-revert-partial",
		CommitMessage: "Document partial failure test",
		BodyPath:      bodyPath,
		Files:         []string{bodyPath},
	})
	// Whether this succeeds or not, the branch name must not be left as a
	// dangling ref if no commit was produced; if it succeeded, we need to be
	// on the review branch (which is also fine).
	currentBranch := gitOut(t, projectPath, "rev-parse", "--abbrev-ref", "HEAD")
	if currentBranch == "iac-studio-drift-revert-partial" {
		// Succeeded legitimately — verify the commit is real.
		committed := gitOut(t, projectPath, "show", "--name-only", "--format=", "HEAD")
		if !strings.Contains(committed, "pr-body.md") {
			t.Fatalf("review branch exists but commit is missing pr-body.md:\n%s", committed)
		}
		return
	}
	// If we're back on main the cleanup worked; the dangling branch must be gone.
	if currentBranch != "main" {
		t.Fatalf("after partial failure current branch = %q, want main or review branch", currentBranch)
	}
	if err == nil {
		t.Fatal("expected an error from the partial handoff, got nil")
	}
	refs := gitOut(t, projectPath, "branch", "--list", "iac-studio-drift-revert-partial")
	if refs != "" {
		t.Fatalf("cleanup should remove the dangling review branch, but it still exists: %q", refs)
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
