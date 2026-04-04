package git

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Repo provides Git operations for a project directory.
type Repo struct {
	dir string
}

// Open returns a Repo for the given directory. Initializes git if needed.
func Open(dir string) (*Repo, error) {
	r := &Repo{dir: dir}
	if !r.IsRepo() {
		if err := r.Init(); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// Status holds the current state of the repository.
type Status struct {
	Branch     string   `json:"branch"`
	IsClean    bool     `json:"is_clean"`
	Staged     []string `json:"staged"`
	Modified   []string `json:"modified"`
	Untracked  []string `json:"untracked"`
	CommitHash string   `json:"commit_hash"`
	CommitMsg  string   `json:"commit_msg"`
	CommitTime string   `json:"commit_time"`
}

type DiffEntry struct {
	File    string `json:"file"`
	Status  string `json:"status"` // added | modified | deleted
	Diff    string `json:"diff"`   // Unified diff text (truncated for large diffs)
}

type LogEntry struct {
	Hash    string `json:"hash"`
	Short   string `json:"short"`
	Message string `json:"message"`
	Author  string `json:"author"`
	Time    string `json:"time"`
}

// ─── Core Operations ───

func (r *Repo) IsRepo() bool {
	return r.run("rev-parse", "--git-dir") == nil
}

func (r *Repo) Init() error {
	if err := r.run("init"); err != nil {
		return fmt.Errorf("git init: %w", err)
	}
	// Set default branch to main
	r.run("checkout", "-b", "main")
	return nil
}

func (r *Repo) GetStatus() (*Status, error) {
	s := &Status{}

	// Branch
	branch, err := r.output("rev-parse", "--abbrev-ref", "HEAD")
	if err == nil {
		s.Branch = strings.TrimSpace(branch)
	}

	// Status
	statusOut, err := r.output("status", "--porcelain")
	if err != nil {
		return nil, err
	}

	for _, line := range strings.Split(strings.TrimSpace(statusOut), "\n") {
		if len(line) < 3 {
			continue
		}
		prefix := line[:2]
		file := strings.TrimSpace(line[3:])
		switch {
		case strings.Contains(prefix, "?"):
			s.Untracked = append(s.Untracked, file)
		case prefix[0] != ' ':
			s.Staged = append(s.Staged, file)
		case prefix[1] == 'M' || prefix[1] == 'D':
			s.Modified = append(s.Modified, file)
		}
	}

	s.IsClean = len(s.Staged) == 0 && len(s.Modified) == 0 && len(s.Untracked) == 0

	// Last commit
	hash, _ := r.output("rev-parse", "--short", "HEAD")
	s.CommitHash = strings.TrimSpace(hash)
	msg, _ := r.output("log", "-1", "--format=%s")
	s.CommitMsg = strings.TrimSpace(msg)
	commitTime, _ := r.output("log", "-1", "--format=%ci")
	s.CommitTime = strings.TrimSpace(commitTime)

	return s, nil
}

func (r *Repo) Add(files ...string) error {
	args := append([]string{"add"}, files...)
	return r.run(args...)
}

func (r *Repo) AddAll() error {
	return r.run("add", "-A")
}

func (r *Repo) Commit(message string) error {
	if message == "" {
		message = fmt.Sprintf("IaC Studio: update at %s", time.Now().Format("2006-01-02 15:04"))
	}
	return r.run("commit", "-m", message, "--author", "IaC Studio <iac-studio@local>")
}

func (r *Repo) Diff() ([]DiffEntry, error) {
	var entries []DiffEntry

	// Staged diff
	stagedDiff, _ := r.output("diff", "--cached", "--name-status")
	for _, line := range parseStatusLines(stagedDiff) {
		diff, _ := r.output("diff", "--cached", "--", line.file)
		entries = append(entries, DiffEntry{
			File:   line.file,
			Status: line.status,
			Diff:   truncateDiff(diff, 2000),
		})
	}

	// Unstaged diff
	unstagedDiff, _ := r.output("diff", "--name-status")
	for _, line := range parseStatusLines(unstagedDiff) {
		diff, _ := r.output("diff", "--", line.file)
		entries = append(entries, DiffEntry{
			File:   line.file,
			Status: line.status,
			Diff:   truncateDiff(diff, 2000),
		})
	}

	return entries, nil
}

func (r *Repo) Log(limit int) ([]LogEntry, error) {
	if limit <= 0 {
		limit = 20
	}
	out, err := r.output("log", fmt.Sprintf("-%d", limit),
		"--format=%H|%h|%s|%an|%ci")
	if err != nil {
		return nil, err
	}

	var entries []LogEntry
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		parts := strings.SplitN(line, "|", 5)
		if len(parts) < 5 {
			continue
		}
		entries = append(entries, LogEntry{
			Hash:    parts[0],
			Short:   parts[1],
			Message: parts[2],
			Author:  parts[3],
			Time:    parts[4],
		})
	}
	return entries, nil
}

// ─── Branch Operations ───

func (r *Repo) ListBranches() ([]string, error) {
	out, err := r.output("branch", "--format=%(refname:short)")
	if err != nil {
		return nil, err
	}
	var branches []string
	for _, b := range strings.Split(strings.TrimSpace(out), "\n") {
		if b != "" {
			branches = append(branches, b)
		}
	}
	return branches, nil
}

func (r *Repo) Checkout(branch string) error {
	return r.run("checkout", branch)
}

func (r *Repo) CreateBranch(name string) error {
	return r.run("checkout", "-b", name)
}

// ─── Helpers ───

func (r *Repo) run(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), stderr.String(), err)
	}
	return nil
}

func (r *Repo) output(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	out, err := cmd.Output()
	return string(out), err
}

type statusLine struct {
	status string
	file   string
}

func parseStatusLines(raw string) []statusLine {
	var lines []statusLine
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		status := "modified"
		switch parts[0] {
		case "A":
			status = "added"
		case "D":
			status = "deleted"
		case "M":
			status = "modified"
		}
		lines = append(lines, statusLine{status: status, file: parts[1]})
	}
	return lines
}

func truncateDiff(diff string, maxLen int) string {
	if len(diff) <= maxLen {
		return diff
	}
	return diff[:maxLen] + "\n... (truncated)"
}
