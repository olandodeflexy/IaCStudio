package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type AuditDecision struct {
	ID               string    `json:"id"`
	Time             time.Time `json:"time"`
	Tool             string    `json:"tool"`
	Project          string    `json:"project,omitempty"`
	ConnectionID     string    `json:"connection_id,omitempty"`
	ApprovalRequired bool      `json:"approval_required,omitempty"`
	Approved         bool      `json:"approved,omitempty"`
	Mutated          bool      `json:"mutated,omitempty"`
	Decision         string    `json:"decision"`
	Error            string    `json:"error,omitempty"`
}

type AuditLogger struct {
	path string
	now  func() time.Time
}

func NewAuditLogger(projectsDir string, now func() time.Time) *AuditLogger {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &AuditLogger{
		path: filepath.Join(projectsDir, ".iac-studio", "mcp-audit.jsonl"),
		now:  now,
	}
}

func (l *AuditLogger) Append(decision AuditDecision) error {
	if strings.TrimSpace(l.path) == "" {
		return fmt.Errorf("audit log path is empty")
	}
	if decision.Time.IsZero() {
		decision.Time = l.now().UTC()
	}
	decision.ID = auditID(decision)
	data, err := json.Marshal(decision)
	if err != nil {
		return fmt.Errorf("marshal audit event: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return fmt.Errorf("create audit directory: %w", err)
	}
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open audit log: %w", err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write audit log: %w", err)
	}
	return nil
}

func auditID(decision AuditDecision) string {
	base := strings.Join([]string{
		decision.Time.Format("20060102T150405.000000000Z"),
		cleanIDSegment(decision.Tool),
		cleanIDSegment(decision.Project),
		cleanIDSegment(decision.Decision),
	}, "-")
	return strings.Trim(base, "-")
}

func cleanIDSegment(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
