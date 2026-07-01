package agentruns

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
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
	promptHashKeyLen    = 32
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
	hashKey []byte
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

// WithPromptHashKey overrides the per-store HMAC key used for prompt
// fingerprints. It is primarily intended for deterministic tests.
func WithPromptHashKey(key []byte) Option {
	return func(s *Store) {
		if len(key) > 0 {
			s.hashKey = append([]byte(nil), key...)
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
	if len(s.hashKey) == 0 {
		s.hashKey = newPromptHashKey()
	}
	return s
}

func (s *Store) Create(req CreateRequest) (Run, error) {
	project := strings.TrimSpace(req.Project)
	if project == "" {
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
		Project:       project,
		ProviderID:    req.ProviderID,
		Mode:          mode,
		Status:        StatusQueued,
		PromptPreview: truncate(redactText(req.Prompt), maxPromptPreviewLen),
		PromptHash:    hashText(req.Prompt, s.hashKey),
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
	return s.update(id, func(run *Run, now time.Time) error {
		if terminalStatus(run.Status) {
			return ErrTerminated
		}
		run.Status = status
		if status == StatusRunning && run.StartedAt == nil {
			run.StartedAt = timePtr(now)
		}
		if status == StatusCanceled {
			run.Canceled = true
		}
		if terminalStatus(status) && run.CompletedAt == nil {
			run.CompletedAt = timePtr(now)
		}
		return nil
	})
}

func (s *Store) Cancel(id string) (Run, error) {
	return s.update(id, func(run *Run, now time.Time) error {
		if terminalStatus(run.Status) {
			return ErrTerminated
		}
		run.Canceled = true
		run.Status = StatusCanceled
		if run.CompletedAt == nil {
			run.CompletedAt = timePtr(now)
		}
		return nil
	})
}

func (s *Store) Fail(id string, message string) (Run, error) {
	return s.update(id, func(run *Run, now time.Time) error {
		if terminalStatus(run.Status) {
			return ErrTerminated
		}
		run.Status = StatusFailed
		run.Error = truncate(redactText(message), maxLogMessageLen)
		if run.CompletedAt == nil {
			run.CompletedAt = timePtr(now)
		}
		return nil
	})
}

func (s *Store) AddLog(id string, level LogLevel, message string) (Run, error) {
	if !validLogLevel(level) {
		return Run{}, fmt.Errorf("invalid agent log level: %s", level)
	}
	return s.update(id, func(run *Run, now time.Time) error {
		if terminalStatus(run.Status) {
			return ErrTerminated
		}
		run.Logs = append(run.Logs, LogEntry{
			ID:      fmt.Sprintf("log_%06d", len(run.Logs)+1),
			At:      now,
			Level:   level,
			Message: truncate(redactText(message), maxLogMessageLen),
		})
		return nil
	})
}

func (s *Store) AddPatch(id string, patch ProposedPatch) (Run, error) {
	patchPath, err := normalizePatchPath(patch.Path)
	if err != nil {
		return Run{}, err
	}
	return s.update(id, func(run *Run, now time.Time) error {
		if terminalStatus(run.Status) {
			return ErrTerminated
		}
		patch.ID = fmt.Sprintf("patch_%06d", len(run.Patches)+1)
		patch.Path = patchPath
		patch.Summary = truncate(redactText(patch.Summary), maxLogMessageLen)
		patch.Diff = truncate(redactText(patch.Diff), maxPatchDiffLen)
		patch.CreatedAt = now
		run.Patches = append(run.Patches, patch)
		return nil
	})
}

func (s *Store) AddApproval(id string, gate ApprovalGate) (Run, error) {
	if !validApprovalKind(gate.Kind) {
		return Run{}, fmt.Errorf("invalid approval kind: %s", gate.Kind)
	}
	return s.update(id, func(run *Run, now time.Time) error {
		if terminalStatus(run.Status) {
			return ErrTerminated
		}
		gate.ID = fmt.Sprintf("approval_%06d", len(run.Approvals)+1)
		gate.Status = ApprovalPending
		gate.Summary = truncate(redactText(gate.Summary), maxLogMessageLen)
		gate.CreatedAt = now
		gate.DecidedAt = nil
		gate.DecidedBy = ""
		run.Approvals = append(run.Approvals, gate)
		run.Status = StatusWaitingApproval
		return nil
	})
}

func (s *Store) DecideApproval(id, approvalID string, decision ApprovalStatus, decidedBy string) (Run, error) {
	if decision != ApprovalApproved && decision != ApprovalRejected {
		return Run{}, fmt.Errorf("invalid approval decision: %s", decision)
	}
	return s.update(id, func(run *Run, now time.Time) error {
		if terminalStatus(run.Status) {
			return ErrTerminated
		}
		for i := range run.Approvals {
			if run.Approvals[i].ID == approvalID {
				if run.Approvals[i].Status != ApprovalPending {
					return fmt.Errorf("approval gate %q is already decided", approvalID)
				}
				run.Approvals[i].Status = decision
				run.Approvals[i].DecidedAt = timePtr(now)
				run.Approvals[i].DecidedBy = truncate(redactText(decidedBy), maxLogMessageLen)
				if run.Status == StatusWaitingApproval && !hasPendingApprovals(run.Approvals) {
					run.Status = StatusRunning
				}
				return nil
			}
		}
		return ErrApprovalNotFound
	})
}

func hasPendingApprovals(approvals []ApprovalGate) bool {
	for _, approval := range approvals {
		if approval.Status == ApprovalPending {
			return true
		}
	}
	return false
}

func normalizePatchPath(raw string) (string, error) {
	patchPath := strings.TrimSpace(raw)
	if patchPath == "" {
		return "", errors.New("patch path is required")
	}
	if strings.Contains(patchPath, "\\") || hasWindowsDrivePrefix(patchPath) || path.IsAbs(patchPath) {
		return "", ErrUnsafePatchPath
	}
	for _, segment := range strings.Split(patchPath, "/") {
		if segment == ".." {
			return "", ErrUnsafePatchPath
		}
	}
	cleaned := path.Clean(patchPath)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", ErrUnsafePatchPath
	}
	return cleaned, nil
}

func hasWindowsDrivePrefix(p string) bool {
	return len(p) >= 2 && p[1] == ':' && ((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z'))
}

func (s *Store) update(id string, mutate func(*Run, time.Time) error) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[id]
	if !ok {
		return Run{}, ErrNotFound
	}
	now := s.now()
	if err := mutate(run, now); err != nil {
		return Run{}, err
	}
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

var (
	ErrNotFound         = errors.New("agent run not found")
	ErrTerminated       = errors.New("agent run is already in a terminal state")
	ErrApprovalNotFound = errors.New("approval gate not found")
	ErrUnsafePatchPath  = errors.New("patch path must be a relative path within the project")
)

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

func newPromptHashKey() []byte {
	key := make([]byte, promptHashKeyLen)
	if _, err := rand.Read(key); err != nil {
		panic(fmt.Sprintf("generate prompt hash key: %v", err))
	}
	return key
}

func hashText(text string, key []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(text))
	return hex.EncodeToString(mac.Sum(nil))
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?:AKIA|ASIA)[0-9A-Z]{16}`),
	regexp.MustCompile(`(?i)(secret|token|password|api[_-]?key|access[_-]?key(?:[_-]?id)?|secret[_-]?access[_-]?key|session[_-]?token)\s*(?::=|[:=])\s*(?:"[^"]*"|'[^']*'|[^\s]+)`),
}

func redactText(text string) string {
	redacted := text
	for _, pattern := range secretPatterns {
		redacted = pattern.ReplaceAllStringFunc(redacted, func(match string) string {
			upper := strings.ToUpper(match)
			if strings.HasPrefix(upper, "AKIA") || strings.HasPrefix(upper, "ASIA") {
				return "[REDACTED]"
			}
			if replacement := redactKeyValue(match); replacement != "" {
				return replacement
			}
			return "[REDACTED]"
		})
	}
	return redacted
}

func redactKeyValue(match string) string {
	end := redactionPrefixEnd(match)
	if end < 0 {
		return ""
	}
	prefix := strings.TrimRight(match[:end], " \t")
	return prefix + " [REDACTED]"
}

func redactionPrefixEnd(match string) int {
	for i := 0; i < len(match); i++ {
		switch match[i] {
		case ':':
			if i+1 < len(match) && match[i+1] == '=' {
				return i + 2
			}
			return i + 1
		case '=':
			return i + 1
		}
	}
	return -1
}

func truncate(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:safeUTF8PrefixLen(text, limit)]
	}
	return text[:safeUTF8PrefixLen(text, limit-3)] + "..."
}

func safeUTF8PrefixLen(text string, limit int) int {
	if limit <= 0 {
		return 0
	}
	if limit >= len(text) {
		return len(text)
	}
	last := 0
	for i := range text {
		if i > limit {
			break
		}
		last = i
	}
	return last
}

func cloneRun(run Run) Run {
	run.Logs = cloneLogs(run.Logs)
	run.Patches = clonePatches(run.Patches)
	run.Approvals = cloneApprovals(run.Approvals)
	run.StartedAt = cloneTimePtr(run.StartedAt)
	run.CompletedAt = cloneTimePtr(run.CompletedAt)
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
	out := make([]ApprovalGate, len(approvals))
	for i, a := range approvals {
		a.DecidedAt = cloneTimePtr(a.DecidedAt)
		out[i] = a
	}
	return out
}

func cloneTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	copied := *t
	return &copied
}

func timePtr(t time.Time) *time.Time {
	return &t
}
