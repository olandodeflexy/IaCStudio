package mcpairlock

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	attestationStoreFileName   = "mcp-airlock-attestations.json"
	attestationStoreVersion    = 1
	maxAttestationStoreBytes   = 1 << 20
	maxExecutableAttestations  = 1024
	executableDigestByteLength = sha256.Size
)

var (
	ErrAttestationStorePathRequired = errors.New("MCP attestation store path is required")
	ErrInvalidAttestationStore      = errors.New("invalid MCP attestation store")
)

// ExecutableAttestation records an explicitly approved executable identity.
// Runtime trust enforcement is intentionally handled by a separate layer.
type ExecutableAttestation struct {
	ServerID     string                `json:"server_id"`
	LaunchSource string                `json:"launch_source"`
	Fingerprint  ExecutableFingerprint `json:"fingerprint"`
	ApprovedAt   time.Time             `json:"approved_at"`
}

type attestationKey struct {
	serverID     string
	launchSource string
}

type persistedAttestationStore struct {
	Version      int                     `json:"version"`
	Attestations []ExecutableAttestation `json:"attestations"`
}

// ExecutableAttestationStore owns durable executable approvals. It does not
// decide whether an MCP server is trusted or allowed to run.
type ExecutableAttestationStore struct {
	mu           sync.RWMutex
	path         string
	attestations map[attestationKey]ExecutableAttestation
}

// NewExecutableAttestationStore loads the versioned store from IaC Studio's
// private data directory. Missing storage starts empty; malformed storage
// fails closed instead of silently discarding approvals.
func NewExecutableAttestationStore(projectsDir string) (*ExecutableAttestationStore, error) {
	if strings.TrimSpace(projectsDir) == "" {
		return nil, ErrAttestationStorePathRequired
	}
	path := filepath.Join(filepath.Clean(projectsDir), ".iac-studio", attestationStoreFileName)
	attestations, err := loadExecutableAttestations(path)
	if err != nil {
		return nil, err
	}
	return &ExecutableAttestationStore{path: path, attestations: attestations}, nil
}

// Save durably replaces one server and launch-source attestation.
func (s *ExecutableAttestationStore) Save(attestation ExecutableAttestation) error {
	if s == nil {
		return ErrInvalidAttestationStore
	}
	if strings.TrimSpace(s.path) == "" {
		return ErrAttestationStorePathRequired
	}
	attestation.ApprovedAt = attestation.ApprovedAt.UTC()
	if err := validateExecutableAttestation(attestation); err != nil {
		return err
	}
	key := executableAttestationKey(attestation.ServerID, attestation.LaunchSource)

	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := loadExecutableAttestations(s.path)
	if err != nil {
		return err
	}
	if !maps.Equal(current, s.attestations) {
		return fmt.Errorf("%w: store changed since it was loaded", ErrInvalidAttestationStore)
	}
	next := cloneExecutableAttestations(current)
	if _, exists := next[key]; !exists && len(next) >= maxExecutableAttestations {
		return fmt.Errorf("%w: too many attestations", ErrInvalidAttestationStore)
	}
	next[key] = attestation
	if err := persistExecutableAttestations(s.path, next); err != nil {
		return err
	}
	s.attestations = next
	return nil
}

// Get returns a detached attestation for one exact server and launch source.
func (s *ExecutableAttestationStore) Get(serverID, launchSource string) (ExecutableAttestation, bool) {
	if s == nil {
		return ExecutableAttestation{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	attestation, ok := s.attestations[executableAttestationKey(serverID, launchSource)]
	return attestation, ok
}

func loadExecutableAttestations(path string) (map[attestationKey]ExecutableAttestation, error) {
	if err := secureExistingAttestationDir(filepath.Dir(path), true); err != nil {
		return nil, err
	}
	pathInfo, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return make(map[attestationKey]ExecutableAttestation), nil
	}
	if err != nil {
		return nil, fmt.Errorf("open MCP attestation store: %w", err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() || pathInfo.Size() > maxAttestationStoreBytes {
		return nil, ErrInvalidAttestationStore
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open MCP attestation store: %w", err)
	}
	defer func() { _ = file.Close() }()
	openInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect MCP attestation store: %w", err)
	}
	if !os.SameFile(pathInfo, openInfo) || !openInfo.Mode().IsRegular() || openInfo.Size() > maxAttestationStoreBytes {
		return nil, ErrInvalidAttestationStore
	}
	if err := file.Chmod(0o600); err != nil {
		return nil, fmt.Errorf("secure MCP attestation store: %w", err)
	}

	var snapshot persistedAttestationStore
	decoder := json.NewDecoder(io.LimitReader(file, maxAttestationStoreBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return nil, fmt.Errorf("%w: decode snapshot: %v", ErrInvalidAttestationStore, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: trailing data", ErrInvalidAttestationStore)
	}
	if snapshot.Version != attestationStoreVersion {
		return nil, fmt.Errorf("%w: unsupported version", ErrInvalidAttestationStore)
	}
	if snapshot.Attestations == nil {
		return nil, fmt.Errorf("%w: attestations must be an array", ErrInvalidAttestationStore)
	}
	if len(snapshot.Attestations) > maxExecutableAttestations {
		return nil, fmt.Errorf("%w: too many attestations", ErrInvalidAttestationStore)
	}

	attestations := make(map[attestationKey]ExecutableAttestation, len(snapshot.Attestations))
	for index, attestation := range snapshot.Attestations {
		attestation.ApprovedAt = attestation.ApprovedAt.UTC()
		if err := validateExecutableAttestation(attestation); err != nil {
			return nil, fmt.Errorf("%w: attestation[%d]: %v", ErrInvalidAttestationStore, index, err)
		}
		key := executableAttestationKey(attestation.ServerID, attestation.LaunchSource)
		if _, exists := attestations[key]; exists {
			return nil, fmt.Errorf("%w: duplicate server and launch source", ErrInvalidAttestationStore)
		}
		attestations[key] = attestation
	}
	return attestations, nil
}

func persistExecutableAttestations(path string, attestations map[attestationKey]ExecutableAttestation) error {
	snapshot := persistedAttestationStore{
		Version:      attestationStoreVersion,
		Attestations: make([]ExecutableAttestation, 0, len(attestations)),
	}
	for _, attestation := range attestations {
		snapshot.Attestations = append(snapshot.Attestations, attestation)
	}
	sort.Slice(snapshot.Attestations, func(i, j int) bool {
		if snapshot.Attestations[i].ServerID == snapshot.Attestations[j].ServerID {
			return snapshot.Attestations[i].LaunchSource < snapshot.Attestations[j].LaunchSource
		}
		return snapshot.Attestations[i].ServerID < snapshot.Attestations[j].ServerID
	})
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal MCP attestation store: %w", err)
	}
	if len(data)+1 > maxAttestationStoreBytes {
		return fmt.Errorf("%w: snapshot exceeds size limit", ErrInvalidAttestationStore)
	}

	dir := filepath.Dir(path)
	if err := ensurePrivateAttestationDir(dir); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return ErrInvalidAttestationStore
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect MCP attestation store: %w", err)
	}
	if err := writeFileAtomic(path, append(data, '\n')); err != nil {
		return fmt.Errorf("write MCP attestation store: %w", err)
	}
	return nil
}

func ensurePrivateAttestationDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create MCP attestation directory: %w", err)
	}
	return secureExistingAttestationDir(dir, false)
}

func secureExistingAttestationDir(dir string, missingOK bool) error {
	info, err := os.Lstat(dir)
	if missingOK && errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect MCP attestation directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return ErrInvalidAttestationStore
	}
	handle, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open MCP attestation directory: %w", err)
	}
	defer func() { _ = handle.Close() }()
	openInfo, err := handle.Stat()
	if err != nil {
		return fmt.Errorf("inspect MCP attestation directory: %w", err)
	}
	if !os.SameFile(info, openInfo) || !openInfo.IsDir() {
		return ErrInvalidAttestationStore
	}
	if err := handle.Chmod(0o700); err != nil {
		return fmt.Errorf("secure MCP attestation directory: %w", err)
	}
	return nil
}

func validateExecutableAttestation(attestation ExecutableAttestation) error {
	if err := validateToolCallIdentifier("server_id", attestation.ServerID, false); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidAttestationStore, err)
	}
	switch attestation.LaunchSource {
	case LaunchSourceRegistry, LaunchSourceExplicitDefinition, LaunchSourceEnvironmentOverride:
	default:
		return fmt.Errorf("%w: unsupported launch source", ErrInvalidAttestationStore)
	}
	if attestation.Fingerprint.Algorithm != "sha256" {
		return fmt.Errorf("%w: unsupported fingerprint algorithm", ErrInvalidAttestationStore)
	}
	if len(attestation.Fingerprint.Digest) != hex.EncodedLen(executableDigestByteLength) ||
		attestation.Fingerprint.Digest != strings.ToLower(attestation.Fingerprint.Digest) {
		return fmt.Errorf("%w: invalid fingerprint digest", ErrInvalidAttestationStore)
	}
	digest, err := hex.DecodeString(attestation.Fingerprint.Digest)
	if err != nil || len(digest) != executableDigestByteLength {
		return fmt.Errorf("%w: invalid fingerprint digest", ErrInvalidAttestationStore)
	}
	if attestation.ApprovedAt.IsZero() {
		return fmt.Errorf("%w: approval timestamp is required", ErrInvalidAttestationStore)
	}
	return nil
}

func executableAttestationKey(serverID, launchSource string) attestationKey {
	return attestationKey{serverID: serverID, launchSource: launchSource}
}

func cloneExecutableAttestations(source map[attestationKey]ExecutableAttestation) map[attestationKey]ExecutableAttestation {
	cloned := make(map[attestationKey]ExecutableAttestation, len(source))
	for key, attestation := range source {
		cloned[key] = attestation
	}
	return cloned
}
