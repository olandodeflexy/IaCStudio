package agentrouting

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestPersistentPolicyStoreSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	scope, policy := validPolicyStoreInput()
	store, err := NewPersistentPolicyStore(root)
	if err != nil {
		t.Fatalf("NewPersistentPolicyStore(): %v", err)
	}
	if err := store.Save(scope, policy); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	path := filepath.Join(root, ".iac-studio", policyStoreFileName)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q): %v", path, err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("policy store permissions = %o, want 600", info.Mode().Perm())
	}

	restarted, err := NewPersistentPolicyStore(root)
	if err != nil {
		t.Fatalf("NewPersistentPolicyStore() after restart: %v", err)
	}
	stored, err := restarted.Get(scope)
	if err != nil {
		t.Fatalf("Get(): %v", err)
	}
	if stored.Rules[0].Effect != policy.Rules[0].Effect || stored.Rules[0].ToolName != policy.Rules[0].ToolName {
		t.Fatalf("reloaded policy = %+v, want %+v", stored, policy)
	}
}

func TestPersistentPolicyStoreRehardensExistingLockFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows policy store security is ACL-based")
	}
	root := t.TempDir()
	store, err := NewPersistentPolicyStore(root)
	if err != nil {
		t.Fatalf("NewPersistentPolicyStore(): %v", err)
	}
	scope, policy := validPolicyStoreInput()
	if err := store.Save(scope, policy); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	dir := filepath.Join(root, ".iac-studio")
	lockPath := filepath.Join(dir, policyStoreFileName+".lock")
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatalf("Chmod(directory): %v", err)
	}
	if err := os.Chmod(lockPath, 0o666); err != nil {
		t.Fatalf("Chmod(lock): %v", err)
	}
	if err := store.Save(scope, policy); err != nil {
		t.Fatalf("Save() after permission change: %v", err)
	}

	assertFileMode(t, dir, 0o700)
	assertFileMode(t, lockPath, 0o600)
}

func TestPersistentPolicyStoreRejectsSymlinkLockFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks on Windows may require elevated privileges")
	}
	root := t.TempDir()
	dir := filepath.Join(root, ".iac-studio")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("unchanged"), 0o644); err != nil {
		t.Fatalf("WriteFile(target): %v", err)
	}
	lockPath := filepath.Join(dir, policyStoreFileName+".lock")
	if err := os.Symlink(target, lockPath); err != nil {
		t.Fatalf("Symlink(): %v", err)
	}

	store, err := NewPersistentPolicyStore(root)
	if err != nil {
		t.Fatalf("NewPersistentPolicyStore(): %v", err)
	}
	scope, policy := validPolicyStoreInput()
	if err := store.Save(scope, policy); err == nil {
		t.Fatal("Save() succeeded with a symlink lock file")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile(target): %v", err)
	}
	if string(data) != "unchanged" {
		t.Fatalf("target contents = %q, want unchanged", data)
	}
	assertFileMode(t, target, 0o644)
}

func TestPersistentPolicyStoreRejectsSymlinkDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks on Windows may require elevated privileges")
	}

	t.Run("startup", func(t *testing.T) {
		root := t.TempDir()
		target := t.TempDir()
		if err := os.Chmod(target, 0o755); err != nil {
			t.Fatalf("Chmod(target): %v", err)
		}
		if err := os.Symlink(target, filepath.Join(root, ".iac-studio")); err != nil {
			t.Fatalf("Symlink(): %v", err)
		}

		if _, err := NewPersistentPolicyStore(root); !errors.Is(err, ErrInvalidPolicyStore) {
			t.Fatalf("NewPersistentPolicyStore() error = %v, want ErrInvalidPolicyStore", err)
		}
		assertFileMode(t, target, 0o755)
	})

	t.Run("save", func(t *testing.T) {
		root := t.TempDir()
		store, err := NewPersistentPolicyStore(root)
		if err != nil {
			t.Fatalf("NewPersistentPolicyStore(): %v", err)
		}
		target := t.TempDir()
		if err := os.Chmod(target, 0o755); err != nil {
			t.Fatalf("Chmod(target): %v", err)
		}
		if err := os.Symlink(target, filepath.Join(root, ".iac-studio")); err != nil {
			t.Fatalf("Symlink(): %v", err)
		}

		scope, policy := validPolicyStoreInput()
		if err := store.Save(scope, policy); !errors.Is(err, ErrInvalidPolicyStore) {
			t.Fatalf("Save() error = %v, want ErrInvalidPolicyStore", err)
		}
		if _, err := os.Stat(filepath.Join(target, policyStoreFileName)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("policy file in symlink target error = %v, want not found", err)
		}
		assertFileMode(t, target, 0o755)
	})
}

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q): %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s permissions = %o, want %o", path, got, want)
	}
}

func TestPersistentPolicyStoreRejectsInvalidSnapshots(t *testing.T) {
	scope, policy := validPolicyStoreInput()
	crossScoped := persistedPolicyStore{
		Version: policyStoreVersion,
		Policies: []persistedScopedPolicy{{
			Scope:  PolicyScope{Project: "other-project", ProviderID: scope.ProviderID},
			Policy: policy,
		}},
	}
	duplicate := persistedPolicyStore{
		Version: policyStoreVersion,
		Policies: []persistedScopedPolicy{
			{Scope: scope, Policy: policy},
			{Scope: scope, Policy: policy},
		},
	}
	encode := func(snapshot persistedPolicyStore) []byte {
		data, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatalf("Marshal(): %v", err)
		}
		return data
	}

	tests := []struct {
		name string
		data []byte
	}{
		{name: "malformed JSON", data: []byte(`{"version":1,"policies":[`)},
		{name: "unsupported version", data: []byte(`{"version":2,"policies":[]}`)},
		{name: "unknown field", data: []byte(`{"version":1,"policies":[],"extra":true}`)},
		{name: "cross-scoped rule", data: encode(crossScoped)},
		{name: "duplicate scope", data: encode(duplicate)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, ".iac-studio")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatalf("MkdirAll(): %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, policyStoreFileName), test.data, 0o600); err != nil {
				t.Fatalf("WriteFile(): %v", err)
			}
			if _, err := NewPersistentPolicyStore(root); !errors.Is(err, ErrInvalidPolicyStore) {
				t.Fatalf("NewPersistentPolicyStore() error = %v, want ErrInvalidPolicyStore", err)
			}
		})
	}
}

func TestPersistentPolicyStoreReportsDecodeFailure(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".iac-studio")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, policyStoreFileName), []byte(`{"version":1,"policies":[`), 0o600); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}

	_, err := NewPersistentPolicyStore(root)
	if !errors.Is(err, ErrInvalidPolicyStore) {
		t.Fatalf("NewPersistentPolicyStore() error = %v, want ErrInvalidPolicyStore", err)
	}
	if !strings.Contains(err.Error(), "unexpected EOF") {
		t.Fatalf("NewPersistentPolicyStore() error = %q, want decoder detail", err)
	}
}

func TestPersistentPolicyStoreFailedWriteKeepsActiveSnapshot(t *testing.T) {
	scope, policy := validPolicyStoreInput()
	store := NewPolicyStore()
	if err := store.Save(scope, policy); err != nil {
		t.Fatalf("Save(existing): %v", err)
	}

	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("blocked"), 0o600); err != nil {
		t.Fatalf("WriteFile(blocker): %v", err)
	}
	store.path = filepath.Join(blocker, policyStoreFileName)
	replacement := clonePolicy(policy)
	replacement.Rules[0].Effect = EffectDeny
	replacement.Rules[0].ApprovalRequired = false
	if err := store.Save(scope, replacement); err == nil {
		t.Fatal("Save(replacement) succeeded with an invalid storage path")
	}

	stored, err := store.Get(scope)
	if err != nil {
		t.Fatalf("Get(): %v", err)
	}
	if stored.Rules[0].Effect != EffectAllow {
		t.Fatalf("failed persistence replaced active policy: %+v", stored)
	}
}

func TestPersistentPolicyStoreRejectsOversizedSnapshotBeforeMutation(t *testing.T) {
	scope, policy := validPolicyStoreInput()
	store, err := NewPersistentPolicyStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewPersistentPolicyStore(): %v", err)
	}
	if err := store.Save(scope, policy); err != nil {
		t.Fatalf("Save(existing): %v", err)
	}

	replacement := clonePolicy(policy)
	replacement.Rules[0].ToolName = strings.Repeat("x", maxPolicyStoreBytes)
	if err := store.Save(scope, replacement); !errors.Is(err, ErrInvalidPolicyStore) {
		t.Fatalf("Save(oversized) error = %v, want ErrInvalidPolicyStore", err)
	}
	stored, err := store.Get(scope)
	if err != nil {
		t.Fatalf("Get(): %v", err)
	}
	if stored.Rules[0].ToolName != policy.Rules[0].ToolName {
		t.Fatalf("oversized persistence replaced active policy")
	}
}

func TestPersistentPolicyStoreConcurrentSavesSurviveRestart(t *testing.T) {
	root := t.TempDir()
	store, err := NewPersistentPolicyStore(root)
	if err != nil {
		t.Fatalf("NewPersistentPolicyStore(): %v", err)
	}

	const count = 12
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for index := range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rule := validRule()
			rule.Project = fmt.Sprintf("project-%02d", index)
			rule.ProviderID = fmt.Sprintf("provider-%02d", index)
			scope := PolicyScope{Project: rule.Project, ProviderID: rule.ProviderID}
			errs <- store.Save(scope, Policy{Rules: []Rule{rule}})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Save(): %v", err)
		}
	}

	restarted, err := NewPersistentPolicyStore(root)
	if err != nil {
		t.Fatalf("NewPersistentPolicyStore() after concurrent saves: %v", err)
	}
	for index := range count {
		scope := PolicyScope{
			Project:    fmt.Sprintf("project-%02d", index),
			ProviderID: fmt.Sprintf("provider-%02d", index),
		}
		if _, err := restarted.Get(scope); err != nil {
			t.Fatalf("Get(%+v): %v", scope, err)
		}
	}
}

func TestPersistentPolicyStoreMergesLatestDurableSnapshotAcrossStores(t *testing.T) {
	root := t.TempDir()
	first, err := NewPersistentPolicyStore(root)
	if err != nil {
		t.Fatalf("NewPersistentPolicyStore(first): %v", err)
	}
	second, err := NewPersistentPolicyStore(root)
	if err != nil {
		t.Fatalf("NewPersistentPolicyStore(second): %v", err)
	}

	firstRule := validRule()
	firstRule.Project = "project-a"
	firstRule.ProviderID = "provider-a"
	firstScope := PolicyScope{Project: firstRule.Project, ProviderID: firstRule.ProviderID}
	if err := first.Save(firstScope, Policy{Rules: []Rule{firstRule}}); err != nil {
		t.Fatalf("first.Save(): %v", err)
	}

	secondRule := validRule()
	secondRule.Project = "project-b"
	secondRule.ProviderID = "provider-b"
	secondScope := PolicyScope{Project: secondRule.Project, ProviderID: secondRule.ProviderID}
	if err := second.Save(secondScope, Policy{Rules: []Rule{secondRule}}); err != nil {
		t.Fatalf("second.Save(): %v", err)
	}

	restarted, err := NewPersistentPolicyStore(root)
	if err != nil {
		t.Fatalf("NewPersistentPolicyStore(restarted): %v", err)
	}
	if _, err := restarted.Get(firstScope); err != nil {
		t.Fatalf("Get(firstScope): %v", err)
	}
	if _, err := restarted.Get(secondScope); err != nil {
		t.Fatalf("Get(secondScope): %v", err)
	}
}

func TestPersistentPolicyStoreRequiresProjectsDirectory(t *testing.T) {
	if _, err := NewPersistentPolicyStore("  "); !errors.Is(err, ErrPolicyStorePathRequired) {
		t.Fatalf("NewPersistentPolicyStore(empty) error = %v, want ErrPolicyStorePathRequired", err)
	}
}
