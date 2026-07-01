package agentruns

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	defaultMaxRuns      = 100
	maxPromptPreviewLen = 240
	maxLogMessageLen    = 2000
	maxPatchDiffLen     = 20000
)

type Status string

const (
	StatusQueued          Status = "queued"
	StatusRunning         Status = "running"
	StatusWaitingApproval Status = "waiting_approval"
	StatusCompleted       Status = "completed"
	StatusFailed          Status = "failed"
	StatusCanceled        Status = "canceled"
)

type Mode string

const (
	ModeReadOnly        Mode = "read_only"
	ModeProposeOnly     Mode = "propose_only"
	ModeApprovedExecute Mode = "approved_execute"
)

type LogLevel string

const (
	LogInfo  LogLevel = "info"
	LogWarn  LogLevel = "warn"
	LogError LogLevel = "error"
	LogAudit LogLevel = "audit"
)

type ApprovalKind string

const (
	ApprovalFileWrite  ApprovalKind = "file_write"
	ApprovalCommand    ApprovalKind = "command"
	ApprovalIaCAction  ApprovalKind = "iac_action"
	ApprovalCloudWrite ApprovalKind = "cloud_write"
	ApprovalSecretRead ApprovalKind = "secret_read"
	ApprovalMCPNetwork ApprovalKind = "mcp_network"
)

type ApprovalStatus string

const (
	ApprovalPending  ApprovalStatus = "pending"
	ApprovalApproved ApprovalStatus = "approved"
	ApprovalRejected ApprovalStatus = "rejected"
)

type CreateRequest struct {
	Project    string
	Prompt     string
	ProviderID string
	Mode       Mode
	CreatedBy  string
}

type Run struct {
	ID            string          `json:"id"`
	Project       string          `json:"project"`
	ProviderID    string          `json:"provider_id,omitempty"`
	Mode          Mode            `json:"mode"`
	Status        Status          `json:"status"`
	PromptPreview string          `json:"prompt_preview"`
	PromptHash    string          `json:"prompt_hash"`
	CreatedBy     string          `json:"created_by,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	StartedAt     *time.Time      `json:"started_at,omitempty"`
	CompletedAt   *time.Time      `json:"completed_at,omitempty"`
	Canceled      bool            `json:"canceled"`
	Error         string          `json:"error,omitempty"`
	Logs          []LogEntry      `json:"logs"`
	Patches       []ProposedPatch `json:"patches"`
	Approvals     []ApprovalGate  `json:"approvals"`
}

type LogEntry struct {
	ID      string    `json:"id"`
	At      time.Time `json:"at"`
	Level   LogLevel  `json:"level"`
	Message string    `json:"message"`
}

type ProposedPatch struct {
	ID        string    `json:"id"`
	Path      string    `json:"path"`
	Summary   string    `json:"summary"`
	Diff      string    `json:"diff"`
	CreatedAt time.Time `json:"created_at"`
}

type ApprovalGate struct {
	ID        string         `json:"id"`
	Kind      ApprovalKind   `json:"kind"`
	Status    ApprovalStatus `json:"status"`
	Summary   string         `json:"summary"`
	CreatedAt time.Time      `json:"created_at"`
	DecidedAt *time.Time     `json:"decided_at,omitempty"`
	DecidedBy string         `json:"decided_by,omitempty"`
}

type Store struct {
	mu      sync.Mutex
	now     func() time.Time
	maxRuns int
	next    uint64
	runs    map[string]*Run
	order   []string
}

type Option func(*Store)

func WithClock(now func() time.Time) Option {
	return func(s *Store) {
		if now != nil {
			s.now = now
		}
	}
}

func WithMaxRuns(maxRun int) Option {
	return func(s *Store) {
		if maxRun > 0 {
			s.maxRuns = maxRun
		}
	}
}

func NewStore(opts ...Option) *Store {
	s := &Store{
		now:     func() time.Time { return time.Now().UTC() },
		maxRuns: defaultMaxRuns,
		runs:    make(map[string]*Run),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Store) Create(req CreateRequest) (Run, error) {
	if strings.TrimSpace(req.Project) == "" {
		return Run{}, errors.New("project is required")
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return Run{}, errors.New("prompt is required")
	}
	mode := req.Mode
	if mode == "" {
		mode = ModeReadOnly
	}
	if !validMode(mode) {
		return Run{}, fmt.Errorf("invalid agent run mode: %s", mode)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	s.next++
	id := fmt.Sprintf("run_%06d", s.next)
	run := &Run{
		ID:            id,
		Project:       req.Project,
		ProviderID:    req.ProviderID,
		Mode:          mode,
		Status:        StatusQueued,
		PromptPreview: truncate(redactText(req.Prompt), maxPromptPreviewLen),
		PromptHash:    hashText(req.Prompt),
		CreatedBy:     req.CreatedBy,
		CreatedAt:     now,
		UpdatedAt:     now,
		Logs:          []LogEntry{},
		Patches:       []ProposedPatch{},
		Approvals:     []ApprovalGate{},
	}
	s.runs[id] = run
	s.order = append(s.order, id)
	s.evictLocked()
	return cloneRun(*run), nil
}

func (s *Store) Get(id string) (Run, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[id]
	if !ok {
		return Run{}, false
	}
	return cloneRun(*run), true
}

func (s *Store) List() []Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	runs := make([]Run, 0, len(s.order))
	for _, id := range s.order {
		if run, ok := s.runs[id]; ok {
			runs = append(runs, cloneRun(*run))
		}
	}
	return runs
}

func (s *Store) SetStatus(id string, status Status) (Run, error) {
	if !validStatus(status) {
		return Run{}, fmt.Errorf("invalid agent run status: %s", status)
	}
	return s.update(id, func(run *Run, now time.Time) {
		run.Status = status
		if status == StatusRunning && run.StartedAt == nil {
			run.StartedAt = timePtr(now)
		}
		if terminalStatus(status) && run.CompletedAt == nil {
			run.CompletedAt = timePtr(now)
		}
	})
}

func (s *Store) Cancel(id string) (Run, error) {
	return s.update(id, func(run *Run, now time.Time) {
		run.Canceled = true
		run.Status = StatusCanceled
		if run.CompletedAt == nil {
			run.CompletedAt = timePtr(now)
		}
	})
}

func (s *Store) Fail(id string, message string) (Run, error) {
	return s.update(id, func(run *Run, now time.Time) {
		run.Status = StatusFailed
		run.Error = truncate(redactText(message), maxLogMessageLen)
		if run.CompletedAt == nil {
			run.CompletedAt = timePtr(now)
		}
	})
}

func (s *Store) AddLog(id string, level LogLevel, message string) (Run, error) {
	if !validLogLevel(level) {
		return Run{}, fmt.Errorf("invalid agent log level: %s", level)
	}
	return s.update(id, func(run *Run, now time.Time) {
		run.Logs = append(run.Logs, LogEntry{
			ID:      fmt.Sprintf("log_%06d", len(run.Logs)+1),
			At:      now,
			Level:   level,
			Message: truncate(redactText(message), maxLogMessageLen),
		})
	})
}

func (s *Store) AddPatch(id string, patch ProposedPatch) (Run, error) {
	if strings.TrimSpace(patch.Path) == "" {
		return Run{}, errors.New("patch path is required")
	}
	return s.update(id, func(run *Run, now time.Time) {
		patch.ID = fmt.Sprintf("patch_%06d", len(run.Patches)+1)
		patch.Summary = truncate(redactText(patch.Summary), maxLogMessageLen)
		patch.Diff = truncate(redactText(patch.Diff), maxPatchDiffLen)
		patch.CreatedAt = now
		run.Patches = append(run.Patches, patch)
	})
}

func (s *Store) AddApproval(id string, gate ApprovalGate) (Run, error) {
	if !validApprovalKind(gate.Kind) {
		return Run{}, fmt.Errorf("invalid approval kind: %s", gate.Kind)
	}
	return s.update(id, func(run *Run, now time.Time) {
		gate.ID = fmt.Sprintf("approval_%06d", len(run.Approvals)+1)
		gate.Status = ApprovalPending
		gate.Summary = truncate(redactText(gate.Summary), maxLogMessageLen)
		gate.CreatedAt = now
		run.Approvals = append(run.Approvals, gate)
		run.Status = StatusWaitingApproval
	})
}

func (s *Store) update(id string, mutate func(*Run, time.Time)) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[id]
	if !ok {
		return Run{}, ErrNotFound
	}
	now := s.now()
	mutate(run, now)
	run.UpdatedAt = now
	return cloneRun(*run), nil
}

func (s *Store) evictLocked() {
	for len(s.order) > s.maxRuns {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.runs, oldest)
	}
}

var ErrNotFound = errors.New("agent run not found")

func validMode(mode Mode) bool {
	switch mode {
	case ModeReadOnly, ModeProposeOnly, ModeApprovedExecute:
		return true
	default:
		return false
	}
}

func validStatus(status Status) bool {
	switch status {
	case StatusQueued, StatusRunning, StatusWaitingApproval, StatusCompleted, StatusFailed, StatusCanceled:
		return true
	default:
		return false
	}
}

func terminalStatus(status Status) bool {
	switch status {
	case StatusCompleted, StatusFailed, StatusCanceled:
		return true
	default:
		return false
	}
}

func validLogLevel(level LogLevel) bool {
	switch level {
	case LogInfo, LogWarn, LogError, LogAudit:
		return true
	default:
		return false
	}
}

func validApprovalKind(kind ApprovalKind) bool {
	switch kind {
	case ApprovalFileWrite, ApprovalCommand, ApprovalIaCAction, ApprovalCloudWrite, ApprovalSecretRead, ApprovalMCPNetwork:
		return true
	default:
		return false
	}
}

func hashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`(?i)(secret|token|password|api[_-]?key|access[_-]?key)\s*[:=]\s*["']?[^"'\s]+`),
}

func redactText(text string) string {
	redacted := text
	for _, pattern := range secretPatterns {
		redacted = pattern.ReplaceAllStringFunc(redacted, func(match string) string {
			if strings.HasPrefix(match, "AKIA") {
				return "[REDACTED]"
			}
			if idx := strings.IndexAny(match, ":="); idx >= 0 {
				return match[:idx+1] + "[REDACTED]"
			}
			return "[REDACTED]"
		})
	}
	return redacted
}

func truncate(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:limit]
	}
	return text[:limit-3] + "..."
}

func cloneRun(run Run) Run {
	run.Logs = cloneLogs(run.Logs)
	run.Patches = clonePatches(run.Patches)
	run.Approvals = cloneApprovals(run.Approvals)
	return run
}

func cloneLogs(logs []LogEntry) []LogEntry {
	if logs == nil {
		return nil
	}
	return append([]LogEntry{}, logs...)
}

func clonePatches(patches []ProposedPatch) []ProposedPatch {
	if patches == nil {
		return nil
	}
	return append([]ProposedPatch{}, patches...)
}

func cloneApprovals(approvals []ApprovalGate) []ApprovalGate {
	if approvals == nil {
		return nil
	}
	return append([]ApprovalGate{}, approvals...)
}

func timePtr(t time.Time) *time.Time {
	return &t
}
