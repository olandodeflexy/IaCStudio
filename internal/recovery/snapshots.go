package recovery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const snapshotsDir = ".iac-studio/snapshots"

// StateSnapshot is the first recovery primitive: a durable audit record for a
// successful state-changing run. It stores metadata and hashes, not raw state.
type StateSnapshot struct {
	ID        string    `json:"id"`
	Project   string    `json:"project"`
	Tool      string    `json:"tool"`
	Env       string    `json:"env,omitempty"`
	Command   string    `json:"command"`
	WorkDir   string    `json:"work_dir"`
	StatePath string    `json:"state_path,omitempty"`
	StateSHA  string    `json:"state_sha256,omitempty"`
	StateSize int64     `json:"state_size,omitempty"`
	PlanPath  string    `json:"plan_path,omitempty"`
	PlanSHA   string    `json:"plan_sha256,omitempty"`
	PlanSize  int64     `json:"plan_size,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Status    string    `json:"status"`
	Notes     []string  `json:"notes,omitempty"`
}

type SnapshotInput struct {
	Project string
	Tool    string
	Env     string
	Command string
}

func RecordSnapshot(projectRoot, workDir string, input SnapshotInput, now time.Time) (StateSnapshot, error) {
	snapshot, err := BuildSnapshot(projectRoot, workDir, input, now)
	if err != nil {
		return StateSnapshot{}, err
	}
	dir := filepath.Join(projectRoot, filepath.FromSlash(snapshotsDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return StateSnapshot{}, fmt.Errorf("create snapshots directory: %w", err)
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return StateSnapshot{}, fmt.Errorf("marshal snapshot: %w", err)
	}
	path := filepath.Join(dir, snapshot.ID+".json")
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return StateSnapshot{}, fmt.Errorf("write snapshot metadata: %w", err)
	}
	return snapshot, nil
}

func BuildSnapshot(projectRoot, workDir string, input SnapshotInput, now time.Time) (StateSnapshot, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	rootAbs, workAbs, relWorkDir, err := containedWorkDir(projectRoot, workDir)
	if err != nil {
		return StateSnapshot{}, err
	}
	if input.Project == "" {
		input.Project = filepath.Base(projectRoot)
	}
	snapshot := StateSnapshot{
		Project:   input.Project,
		Tool:      input.Tool,
		Env:       input.Env,
		Command:   input.Command,
		WorkDir:   relWorkDir,
		CreatedAt: now,
		Status:    "recorded",
	}

	if file, ok, hashErr := firstExistingHash(rootAbs, workAbs, []string{"terraform.tfstate"}); hashErr != nil {
		return StateSnapshot{}, hashErr
	} else if ok {
		snapshot.StatePath = file.Path
		snapshot.StateSHA = file.SHA256
		snapshot.StateSize = file.Size
	} else {
		snapshot.Notes = append(snapshot.Notes, stateNote(input.Tool))
	}

	if file, ok, hashErr := firstExistingHash(rootAbs, workAbs, []string{"tfplan.json", "tfplan"}); hashErr != nil {
		return StateSnapshot{}, hashErr
	} else if ok {
		snapshot.PlanPath = file.Path
		snapshot.PlanSHA = file.SHA256
		snapshot.PlanSize = file.Size
	}

	snapshot.ID = snapshotID(snapshot)
	return snapshot, nil
}

func ListSnapshots(projectRoot string) ([]StateSnapshot, error) {
	dir := filepath.Join(projectRoot, filepath.FromSlash(snapshotsDir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []StateSnapshot{}, nil
		}
		return nil, fmt.Errorf("read snapshots directory: %w", err)
	}

	snapshots := make([]StateSnapshot, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read snapshot %s: %w", entry.Name(), err)
		}
		var snapshot StateSnapshot
		if err := json.Unmarshal(data, &snapshot); err != nil {
			return nil, fmt.Errorf("parse snapshot %s: %w", entry.Name(), err)
		}
		snapshots = append(snapshots, snapshot)
	}

	sort.SliceStable(snapshots, func(i, j int) bool {
		return snapshots[i].CreatedAt.After(snapshots[j].CreatedAt)
	})
	return snapshots, nil
}

type hashedFile struct {
	Path   string
	SHA256 string
	Size   int64
}

func containedWorkDir(projectRoot, workDir string) (string, string, string, error) {
	rootAbs, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", "", "", fmt.Errorf("resolve project root: %w", err)
	}
	workAbs, err := filepath.Abs(workDir)
	if err != nil {
		return "", "", "", fmt.Errorf("resolve work dir: %w", err)
	}
	rel, err := filepath.Rel(rootAbs, workAbs)
	if err != nil {
		return "", "", "", fmt.Errorf("relativize work dir: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", "", "", fmt.Errorf("work dir escapes project root")
	}
	if rel == "." {
		rel = ""
	}
	return rootAbs, workAbs, filepath.ToSlash(rel), nil
}

func firstExistingHash(rootAbs, workAbs string, candidates []string) (hashedFile, bool, error) {
	for _, candidate := range candidates {
		path := filepath.Join(workAbs, candidate)
		info, err := os.Lstat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return hashedFile{}, false, fmt.Errorf("stat %s: %w", candidate, err)
		}
		if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return hashedFile{}, false, fmt.Errorf("read %s: %w", candidate, err)
		}
		rel, err := filepath.Rel(rootAbs, path)
		if err != nil {
			return hashedFile{}, false, fmt.Errorf("relativize %s: %w", candidate, err)
		}
		sum := sha256.Sum256(data)
		return hashedFile{
			Path:   filepath.ToSlash(rel),
			SHA256: hex.EncodeToString(sum[:]),
			Size:   info.Size(),
		}, true, nil
	}
	return hashedFile{}, false, nil
}

func stateNote(tool string) string {
	switch tool {
	case "pulumi":
		return "No local Terraform/OpenTofu state file was found; Pulumi state is usually stored in the configured backend."
	case "ansible":
		return "No local state file was found; Ansible runs are recorded as operational checkpoints."
	default:
		return "No local terraform.tfstate file was found in the run directory."
	}
}

func snapshotID(snapshot StateSnapshot) string {
	env := snapshot.Env
	if env == "" {
		env = "root"
	}
	fingerprint := sha256.Sum256([]byte(strings.Join([]string{
		snapshot.Project,
		snapshot.Tool,
		snapshot.Command,
		env,
		snapshot.WorkDir,
		snapshot.StateSHA,
		snapshot.PlanSHA,
		snapshot.CreatedAt.Format(time.RFC3339Nano),
	}, "|")))
	parts := []string{
		snapshot.CreatedAt.Format("20060102T150405Z"),
		slug(snapshot.Tool),
		slug(snapshot.Command),
		slug(env),
		hex.EncodeToString(fingerprint[:])[:8],
	}
	return strings.Join(parts, "-")
}

func slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "unknown"
	}
	return out
}
