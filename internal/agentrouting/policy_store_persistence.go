package agentrouting

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	policyStoreFileName = "agent-routing-policies.json"
	policyStoreVersion  = 1
	maxPolicyStoreBytes = 4 << 20
)

var (
	ErrPolicyStorePathRequired = errors.New("tool route policy store path is required")
	ErrInvalidPolicyStore      = errors.New("invalid persisted tool route policy store")
)

type persistedPolicyStore struct {
	Version  int                     `json:"version"`
	Policies []persistedScopedPolicy `json:"policies"`
}

type persistedScopedPolicy struct {
	Scope  PolicyScope `json:"scope"`
	Policy Policy      `json:"policy"`
}

// NewPersistentPolicyStore loads validated routing policies from the shared
// IaC Studio data directory. Missing storage starts empty; malformed storage
// fails startup rather than silently discarding authorization policy.
func NewPersistentPolicyStore(projectsDir string) (*PolicyStore, error) {
	if strings.TrimSpace(projectsDir) == "" {
		return nil, ErrPolicyStorePathRequired
	}
	// Preserve the configured path exactly; leading and trailing spaces are
	// valid filesystem path characters.
	path := filepath.Join(filepath.Clean(projectsDir), ".iac-studio", policyStoreFileName)
	if err := secureExistingPolicyStoreDir(filepath.Dir(path), true); err != nil {
		return nil, err
	}
	policies, err := loadPolicyStore(path)
	if err != nil {
		return nil, err
	}
	return &PolicyStore{policies: policies, path: path}, nil
}

func loadPolicyStore(path string) (map[PolicyScope]Policy, error) {
	file, err := openPolicyStoreDataFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return make(map[PolicyScope]Policy), nil
	}
	if err != nil {
		return nil, fmt.Errorf("open tool route policy store: %w", err)
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect tool route policy store: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxPolicyStoreBytes {
		return nil, ErrInvalidPolicyStore
	}
	if err := securePolicyStoreHandle(file); err != nil {
		return nil, fmt.Errorf("secure tool route policy store: %w", err)
	}

	var snapshot persistedPolicyStore
	decoder := json.NewDecoder(io.LimitReader(file, maxPolicyStoreBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return nil, fmt.Errorf("%w: decode snapshot: %v", ErrInvalidPolicyStore, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: trailing data", ErrInvalidPolicyStore)
	}
	if snapshot.Version != policyStoreVersion {
		return nil, fmt.Errorf("%w: unsupported version", ErrInvalidPolicyStore)
	}

	policies := make(map[PolicyScope]Policy, len(snapshot.Policies))
	for index, entry := range snapshot.Policies {
		if err := validateStoredPolicy(entry); err != nil {
			return nil, fmt.Errorf("%w: policy[%d]: %v", ErrInvalidPolicyStore, index, err)
		}
		if _, exists := policies[entry.Scope]; exists {
			return nil, fmt.Errorf("%w: duplicate policy scope", ErrInvalidPolicyStore)
		}
		policies[entry.Scope] = clonePolicy(entry.Policy)
	}
	return policies, nil
}

func validateStoredPolicy(entry persistedScopedPolicy) error {
	if err := entry.Scope.Validate(); err != nil {
		return err
	}
	if err := entry.Policy.Validate(); err != nil {
		return err
	}
	for _, rule := range entry.Policy.Rules {
		if rule.Project != entry.Scope.Project || rule.ProviderID != entry.Scope.ProviderID {
			return ErrPolicyScopeMismatch
		}
	}
	return nil
}

func persistPolicyStore(path string, policies map[PolicyScope]Policy) error {
	snapshot := persistedPolicyStore{
		Version:  policyStoreVersion,
		Policies: make([]persistedScopedPolicy, 0, len(policies)),
	}
	for scope, policy := range policies {
		snapshot.Policies = append(snapshot.Policies, persistedScopedPolicy{
			Scope:  scope,
			Policy: clonePolicy(policy),
		})
	}
	sort.Slice(snapshot.Policies, func(i, j int) bool {
		if snapshot.Policies[i].Scope.Project == snapshot.Policies[j].Scope.Project {
			return snapshot.Policies[i].Scope.ProviderID < snapshot.Policies[j].Scope.ProviderID
		}
		return snapshot.Policies[i].Scope.Project < snapshot.Policies[j].Scope.Project
	})

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal tool route policy store: %w", err)
	}
	if len(data)+1 > maxPolicyStoreBytes {
		return fmt.Errorf("%w: snapshot exceeds size limit", ErrInvalidPolicyStore)
	}
	if err := writePolicyStoreAtomic(path, append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func persistPolicyStoreLocked(path string, scope PolicyScope, policy Policy) error {
	dir := filepath.Dir(path)
	if err := ensurePolicyStoreDir(dir); err != nil {
		return err
	}

	lock, err := openPolicyStoreLock(path)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	if err := lockPolicyStoreFile(lock); err != nil {
		return err
	}
	defer func() { _ = unlockPolicyStoreFile(lock) }()

	policies, err := loadPolicyStore(path)
	if err != nil {
		return err
	}
	policies[scope] = clonePolicy(policy)
	return persistPolicyStore(path, policies)
}

func ensurePolicyStoreDir(dir string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return fmt.Errorf("create tool route policy parent directory: %w", err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create tool route policy directory: %w", err)
	}
	return secureExistingPolicyStoreDir(dir, false)
}

func secureExistingPolicyStoreDir(dir string, missingOK bool) error {
	handle, err := openPolicyStoreDirFile(dir)
	if missingOK && errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: inspect policy store directory: %v", ErrInvalidPolicyStore, err)
	}
	defer func() { _ = handle.Close() }()
	if err := securePolicyStoreDirHandle(handle); err != nil {
		return fmt.Errorf("secure tool route policy directory: %w", err)
	}
	return nil
}

func writePolicyStoreAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary policy store: %w", err)
	}
	tmpPath := tmp.Name()
	keepTemp := true
	defer func() {
		if keepTemp {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secure temporary policy store: %w", err)
	}
	if err := securePolicyStoreFile(tmpPath); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secure temporary policy store: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary policy store: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temporary policy store: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary policy store: %w", err)
	}
	if err := replacePolicyStoreFile(tmpPath, path); err != nil {
		return fmt.Errorf("replace tool route policy store: %w", err)
	}
	keepTemp = false
	syncPolicyStoreDirBestEffort(dir)
	return nil
}

func openPolicyStoreLock(path string) (*os.File, error) {
	lockPath := path + ".lock"
	handle, err := openPolicyStoreLockFile(lockPath)
	if err != nil {
		return nil, fmt.Errorf("open policy store lock: %w", err)
	}
	if err := securePolicyStoreHandle(handle); err != nil {
		_ = handle.Close()
		return nil, fmt.Errorf("secure policy store lock: %w", err)
	}
	return handle, nil
}

func syncPolicyStoreDirBestEffort(dir string) {
	handle, err := os.Open(dir)
	if err != nil {
		return
	}
	defer func() { _ = handle.Close() }()
	_ = handle.Sync()
}
