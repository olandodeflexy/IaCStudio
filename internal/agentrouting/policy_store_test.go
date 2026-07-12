package agentrouting

import (
	"errors"
	"sync"
	"testing"

	"github.com/iac-studio/iac-studio/internal/agentruns"
)

func validPolicyStoreInput() (PolicyScope, Policy) {
	rule := validRule()
	return PolicyScope{Project: rule.Project, ProviderID: rule.ProviderID}, Policy{Rules: []Rule{rule}}
}

func TestPolicyStoreReturnsImmutableScopedSnapshots(t *testing.T) {
	scope, policy := validPolicyStoreInput()
	store := NewPolicyStore()
	if err := store.Save(scope, policy); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	policy.Rules[0].Effect = EffectDeny
	policy.Rules[0].Modes[0] = "invalid"
	stored, err := store.Get(scope)
	if err != nil {
		t.Fatalf("Get(): %v", err)
	}
	if stored.Rules[0].Effect != EffectAllow || stored.Rules[0].Modes[0] != agentruns.ModeProposeOnly {
		t.Fatalf("stored policy changed with Save caller: %+v", stored)
	}

	stored.Rules[0].Effect = EffectDeny
	stored.Rules[0].Modes[0] = "invalid"
	again, err := store.Get(scope)
	if err != nil {
		t.Fatalf("Get() again: %v", err)
	}
	if again.Rules[0].Effect != EffectAllow || again.Rules[0].Modes[0] != agentruns.ModeProposeOnly {
		t.Fatalf("stored policy changed with Get caller: %+v", again)
	}
}

func TestPolicyStoreRejectsInvalidReplacementWithoutMutation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Policy)
		wantErr error
	}{
		{
			name: "cross-project rule",
			mutate: func(policy *Policy) {
				policy.Rules[0].Project = "other-project"
			},
			wantErr: ErrPolicyScopeMismatch,
		},
		{
			name: "cross-provider rule",
			mutate: func(policy *Policy) {
				policy.Rules[0].ProviderID = "other-provider"
			},
			wantErr: ErrPolicyScopeMismatch,
		},
		{
			name: "invalid rule",
			mutate: func(policy *Policy) {
				policy.Rules[0].Effect = "unsupported"
			},
			wantErr: ErrInvalidRule,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scope, existing := validPolicyStoreInput()
			store := NewPolicyStore()
			if err := store.Save(scope, existing); err != nil {
				t.Fatalf("Save(existing): %v", err)
			}
			candidate := clonePolicy(existing)
			test.mutate(&candidate)

			if err := store.Save(scope, candidate); !errors.Is(err, test.wantErr) {
				t.Fatalf("Save(invalid) error = %v, want %v", err, test.wantErr)
			}
			stored, err := store.Get(scope)
			if err != nil {
				t.Fatalf("Get(): %v", err)
			}
			if stored.Rules[0].Effect != EffectAllow || stored.Rules[0].Project != scope.Project || stored.Rules[0].ProviderID != scope.ProviderID {
				t.Fatalf("invalid replacement mutated stored policy: %+v", stored)
			}
		})
	}
}

func TestPolicyStoreFailsClosedForMissingOrInvalidScopes(t *testing.T) {
	scope, policy := validPolicyStoreInput()
	store := NewPolicyStore()
	if _, err := store.Get(scope); !errors.Is(err, ErrPolicyNotFound) {
		t.Fatalf("Get(missing) error = %v, want ErrPolicyNotFound", err)
	}

	invalidScopes := []PolicyScope{
		{ProviderID: scope.ProviderID},
		{Project: scope.Project, ProviderID: " " + scope.ProviderID},
	}
	for _, invalid := range invalidScopes {
		if err := store.Save(invalid, policy); !errors.Is(err, ErrInvalidPolicyScope) {
			t.Fatalf("Save(%+v) error = %v, want ErrInvalidPolicyScope", invalid, err)
		}
		if _, err := store.Get(invalid); !errors.Is(err, ErrInvalidPolicyScope) {
			t.Fatalf("Get(%+v) error = %v, want ErrInvalidPolicyScope", invalid, err)
		}
	}

	var nilStore *PolicyStore
	if err := nilStore.Save(scope, policy); !errors.Is(err, ErrPolicyStoreRequired) {
		t.Fatalf("nil Save() error = %v, want ErrPolicyStoreRequired", err)
	}
	if _, err := nilStore.Get(scope); !errors.Is(err, ErrPolicyStoreRequired) {
		t.Fatalf("nil Get() error = %v, want ErrPolicyStoreRequired", err)
	}
}

func TestPolicyStoreConcurrentSaveAndGetAreRaceFree(t *testing.T) {
	scope, policy := validPolicyStoreInput()
	store := NewPolicyStore()
	if err := store.Save(scope, policy); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	const goroutines = 50
	var wg sync.WaitGroup
	errs := make(chan error, goroutines*2)
	wg.Add(goroutines * 2)
	for range goroutines {
		go func() {
			defer wg.Done()
			errs <- store.Save(scope, policy)
		}()
		go func() {
			defer wg.Done()
			_, err := store.Get(scope)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent policy-store operation failed: %v", err)
		}
	}
}
