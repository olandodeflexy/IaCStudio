package review

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

var (
	ErrBranchExists     = errors.New("review branch already exists")
	ErrDirtyWorktree    = errors.New("project has unrelated uncommitted changes")
	ErrInvalidInput     = errors.New("invalid pull request handoff input")
	ErrNoChanges        = errors.New("no generated artifact changes to commit")
	ErrNotGitRepository = errors.New("project is not a git repository")
)

type Command struct {
	Label   string   `json:"label"`
	Args    []string `json:"args"`
	Display string   `json:"display"`
}

type PullRequestHandoff struct {
	Title         string    `json:"title"`
	Branch        string    `json:"branch"`
	BaseBranch    string    `json:"base_branch"`
	Commit        string    `json:"commit"`
	CommitMessage string    `json:"commit_message"`
	BodyPath      string    `json:"body_path"`
	Files         []string  `json:"files"`
	Commands      []Command `json:"commands"`
	Warnings      []string  `json:"warnings,omitempty"`
}

type PullRequestHandoffInput struct {
	ProjectPath   string
	Title         string
	Branch        string
	CommitMessage string
	BodyPath      string
	Files         []string
}

func CreatePullRequestHandoff(input PullRequestHandoffInput) (PullRequestHandoff, error) {
	projectPath, err := filepath.Abs(strings.TrimSpace(input.ProjectPath))
	if err != nil || projectPath == "" {
		return PullRequestHandoff{}, fmt.Errorf("%w: project path is required", ErrInvalidInput)
	}
	if !isGitRepo(projectPath) {
		return PullRequestHandoff{}, ErrNotGitRepository
	}
	branch, err := validateBranch(input.Branch)
	if err != nil {
		return PullRequestHandoff{}, err
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		return PullRequestHandoff{}, fmt.Errorf("%w: pull request title is required", ErrInvalidInput)
	}
	commitMessage := strings.TrimSpace(input.CommitMessage)
	if commitMessage == "" {
		return PullRequestHandoff{}, fmt.Errorf("%w: commit message is required", ErrInvalidInput)
	}
	if containsControl(commitMessage) || containsControl(title) {
		return PullRequestHandoff{}, fmt.Errorf("%w: title and commit message cannot contain control characters", ErrInvalidInput)
	}

	files, err := normalizeArtifactFiles(input.Files)
	if err != nil {
		return PullRequestHandoff{}, err
	}
	bodyPath, err := normalizeArtifactPath(input.BodyPath)
	if err != nil {
		return PullRequestHandoff{}, err
	}
	if !containsPath(files, bodyPath) {
		return PullRequestHandoff{}, fmt.Errorf("%w: body path must be one of the generated artifact files", ErrInvalidInput)
	}
	for _, file := range files {
		if err := ensureRegularArtifactFile(projectPath, file); err != nil {
			return PullRequestHandoff{}, err
		}
	}

	if err := EnsureNoUnrelatedChanges(projectPath, files); err != nil {
		return PullRequestHandoff{}, err
	}
	changed, err := ChangedPaths(projectPath)
	if err != nil {
		return PullRequestHandoff{}, err
	}
	if !containsAnyPath(changed, files) {
		return PullRequestHandoff{}, ErrNoChanges
	}
	if branchExists(projectPath, branch) {
		return PullRequestHandoff{}, fmt.Errorf("%w: %s", ErrBranchExists, branch)
	}

	baseBranch, err := gitOutput(projectPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return PullRequestHandoff{}, fmt.Errorf("detecting current branch: %w", err)
	}
	baseBranch = strings.TrimSpace(baseBranch)
	if baseBranch == "" || baseBranch == "HEAD" {
		baseBranch = "main"
	}

	if err := gitRun(projectPath, "checkout", "-b", branch); err != nil {
		return PullRequestHandoff{}, err
	}
	// cleanupBranch switches back to baseBranch and removes the review branch if
	// any step after checkout fails, so the caller is never left stranded on an
	// uncommitted review branch.
	cleanupBranch := func() {
		_ = gitRun(projectPath, "checkout", baseBranch)
		_ = gitRun(projectPath, "branch", "-D", branch)
	}
	addArgs := append([]string{"add", "--"}, files...)
	if err := gitRun(projectPath, addArgs...); err != nil {
		cleanupBranch()
		return PullRequestHandoff{}, err
	}
	hasChanges, err := hasStagedChanges(projectPath)
	if err != nil {
		cleanupBranch()
		return PullRequestHandoff{}, err
	}
	if !hasChanges {
		cleanupBranch()
		return PullRequestHandoff{}, ErrNoChanges
	}
	if err := gitRun(projectPath, "commit", "-m", commitMessage, "--author", "IaC Studio <iac-studio@local>"); err != nil {
		cleanupBranch()
		return PullRequestHandoff{}, err
	}
	commit, err := gitOutput(projectPath, "rev-parse", "HEAD")
	if err != nil {
		return PullRequestHandoff{}, fmt.Errorf("reading review commit: %w", err)
	}
	commit = strings.TrimSpace(commit)

	warnings := []string{}
	if _, err := gitOutput(projectPath, "remote", "get-url", "origin"); err != nil {
		warnings = append(warnings, "No origin remote is configured; add one before pushing this review branch.")
	}
	if remaining, err := ChangedPaths(projectPath); err == nil {
		if ignored := ignoredDirtyPaths(remaining); len(ignored) > 0 {
			warnings = append(warnings, "Local runtime files were left uncommitted: "+strings.Join(ignored, ", "))
		}
	}
	return PullRequestHandoff{
		Title:         title,
		Branch:        branch,
		BaseBranch:    baseBranch,
		Commit:        commit,
		CommitMessage: commitMessage,
		BodyPath:      bodyPath,
		Files:         files,
		Commands:      reviewCommands(baseBranch, branch, title, bodyPath),
		Warnings:      warnings,
	}, nil
}

func EnsureNoUnrelatedChanges(projectPath string, allowedFiles []string) error {
	projectPath, err := filepath.Abs(strings.TrimSpace(projectPath))
	if err != nil || projectPath == "" {
		return fmt.Errorf("%w: project path is required", ErrInvalidInput)
	}
	if !isGitRepo(projectPath) {
		return ErrNotGitRepository
	}
	allowed, err := normalizeArtifactFiles(allowedFiles)
	if err != nil {
		return err
	}
	changed, err := ChangedPaths(projectPath)
	if err != nil {
		return err
	}
	if !pathsSubset(changed, allowed) {
		return fmt.Errorf("%w: %s", ErrDirtyWorktree, strings.Join(subtractPaths(changed, allowed), ", "))
	}
	return nil
}

func ChangedPaths(projectPath string) ([]string, error) {
	out, err := gitOutput(projectPath, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	records := bytes.Split([]byte(out), []byte{0})
	paths := make([]string, 0, len(records))
	for i := 0; i < len(records); i++ {
		record := string(records[i])
		if len(record) < 4 {
			continue
		}
		path := record[3:]
		if strings.ContainsAny(record[:2], "RC") && i+1 < len(records) && len(records[i+1]) > 0 {
			i++
			path = string(records[i])
		}
		normalized, err := normalizeRepoPath(path)
		if err != nil {
			return nil, err
		}
		paths = append(paths, normalized)
	}
	sort.Strings(paths)
	return paths, nil
}

func normalizeArtifactFiles(files []string) ([]string, error) {
	seen := map[string]bool{}
	normalized := make([]string, 0, len(files))
	for _, file := range files {
		path, err := normalizeArtifactPath(file)
		if err != nil {
			return nil, err
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		normalized = append(normalized, path)
	}
	if len(normalized) == 0 {
		return nil, fmt.Errorf("%w: at least one generated artifact file is required", ErrInvalidInput)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func normalizeArtifactPath(path string) (string, error) {
	normalized, err := normalizeRepoPath(path)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(normalized, ".iac-studio/remediations/") &&
		!strings.HasPrefix(normalized, ".iac-studio/rollbacks/") {
		return "", fmt.Errorf("%w: only generated IaC Studio review artifacts can be committed: %s", ErrInvalidInput, path)
	}
	return normalized, nil
}

func normalizeRepoPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" || strings.ContainsRune(path, 0) {
		return "", fmt.Errorf("%w: empty or invalid file path", ErrInvalidInput)
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("%w: unsafe file path %q", ErrInvalidInput, path)
	}
	return filepath.ToSlash(clean), nil
}

func ensureRegularArtifactFile(projectPath, relPath string) error {
	current := projectPath
	parts := strings.Split(relPath, "/")
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("%w: generated artifact %s is not readable: %v", ErrInvalidInput, relPath, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: generated artifact path uses a symlink: %s", ErrInvalidInput, relPath)
		}
		if i < len(parts)-1 {
			if !info.IsDir() {
				return fmt.Errorf("%w: generated artifact parent is not a directory: %s", ErrInvalidInput, relPath)
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: generated artifact is not a regular file: %s", ErrInvalidInput, relPath)
		}
	}
	return nil
}

func validateBranch(value string) (string, error) {
	branch := strings.TrimSpace(value)
	if branch == "" || strings.HasPrefix(branch, "-") || strings.Contains(branch, "..") ||
		strings.Contains(branch, "//") || strings.HasSuffix(branch, "/") ||
		strings.HasSuffix(branch, ".") || strings.Contains(branch, "@{") ||
		strings.ContainsAny(branch, ` ~^:?*[\`) || containsControl(branch) {
		return "", fmt.Errorf("%w: unsafe branch name %q", ErrInvalidInput, value)
	}
	for _, part := range strings.Split(branch, "/") {
		if part == "" || strings.HasPrefix(part, ".") {
			return "", fmt.Errorf("%w: unsafe branch name %q", ErrInvalidInput, value)
		}
	}
	for _, r := range branch {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' || r == '/') {
			return "", fmt.Errorf("%w: unsafe branch name %q", ErrInvalidInput, value)
		}
	}
	return branch, nil
}

func containsControl(value string) bool {
	return strings.ContainsFunc(value, unicode.IsControl)
}

func isGitRepo(projectPath string) bool {
	return gitRun(projectPath, "rev-parse", "--is-inside-work-tree") == nil
}

func branchExists(projectPath, branch string) bool {
	return gitRun(projectPath, "show-ref", "--verify", "--quiet", "refs/heads/"+branch) == nil
}

func reviewCommands(baseBranch, branch, title, bodyPath string) []Command {
	push := []string{"git", "push", "-u", "origin", branch}
	create := []string{"gh", "pr", "create", "--base", baseBranch, "--head", branch, "--title", title, "--body-file", bodyPath}
	return []Command{
		{Label: "Push branch", Args: push, Display: shellDisplay(push)},
		{Label: "Open pull request", Args: create, Display: shellDisplay(create)},
	}
}

func shellDisplay(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "" {
			quoted = append(quoted, "''")
			continue
		}
		if strings.IndexFunc(arg, func(r rune) bool {
			return !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-_./:=@", r))
		}) == -1 {
			quoted = append(quoted, arg)
			continue
		}
		quoted = append(quoted, "'"+strings.ReplaceAll(arg, "'", `'\''`)+"'")
	}
	return strings.Join(quoted, " ")
}

func containsPath(paths []string, target string) bool {
	for _, path := range paths {
		if path == target {
			return true
		}
	}
	return false
}

func containsAnyPath(paths, targets []string) bool {
	targetSet := map[string]bool{}
	for _, target := range targets {
		targetSet[target] = true
	}
	for _, path := range paths {
		if targetSet[path] {
			return true
		}
	}
	return false
}

func pathsSubset(paths, allowed []string) bool {
	allowedSet := map[string]bool{}
	for _, path := range allowed {
		allowedSet[path] = true
	}
	for _, path := range paths {
		if !allowedSet[path] && !isIgnorableDirtyPath(path) {
			return false
		}
	}
	return true
}

func subtractPaths(paths, allowed []string) []string {
	allowedSet := map[string]bool{}
	for _, path := range allowed {
		allowedSet[path] = true
	}
	var out []string
	for _, path := range paths {
		if !allowedSet[path] && !isIgnorableDirtyPath(path) {
			out = append(out, path)
		}
	}
	return out
}

func isIgnorableDirtyPath(path string) bool {
	if strings.HasPrefix(path, ".terraform/") ||
		strings.HasPrefix(path, ".iac-studio/snapshots/") {
		return true
	}
	base := filepath.Base(path)
	return base == "terraform.tfstate" ||
		base == "terraform.tfstate.backup" ||
		base == "tfplan" ||
		base == "tfplan.json" ||
		strings.HasSuffix(base, ".tfstate") ||
		strings.HasSuffix(base, ".tfstate.backup") ||
		strings.HasSuffix(base, ".tfplan")
}

func ignoredDirtyPaths(paths []string) []string {
	var ignored []string
	for _, path := range paths {
		if isIgnorableDirtyPath(path) {
			ignored = append(ignored, path)
		}
	}
	return ignored
}

func gitRun(projectPath string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = projectPath
	cmd.Env = gitEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(stderr.String()), err)
	}
	return nil
}

func hasStagedChanges(projectPath string) (bool, error) {
	cmd := exec.Command("git", "diff", "--cached", "--quiet")
	cmd.Dir = projectPath
	cmd.Env = gitEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return false, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() == 1 {
			return true, nil
		}
	}
	return false, fmt.Errorf("git diff --cached --quiet: %s: %w", strings.TrimSpace(stderr.String()), err)
}

func gitOutput(projectPath string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = projectPath
	cmd.Env = gitEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(stderr.String()), err)
	}
	return string(out), nil
}

func gitEnv() []string {
	return append(os.Environ(),
		"GIT_AUTHOR_NAME=IaC Studio",
		"GIT_AUTHOR_EMAIL=iac-studio@local",
		"GIT_COMMITTER_NAME=IaC Studio",
		"GIT_COMMITTER_EMAIL=iac-studio@local",
	)
}
