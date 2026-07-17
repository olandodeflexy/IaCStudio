package agentrouting

import (
	"errors"
	"fmt"
	"sync"
)

var (
	ErrPolicyStoreRequired = errors.New("tool route policy store is required")
	ErrInvalidPolicyScope  = errors.New("invalid tool route policy scope")
	ErrPolicyNotFound      = errors.New("tool route policy not found")
	ErrPolicyScopeMismatch = errors.New("tool route policy contains a rule outside its scope")
)

// PolicyScope identifies one server-owned route policy. Policies never fall
// back across projects or model providers.
type PolicyScope struct {
	Project    string `json:"project"`
	ProviderID string `json:"provider_id"`
}

func (s PolicyScope) Validate() error {
	return validateRequiredFields(ErrInvalidPolicyScope,
		fieldValue{name: "project", value: s.Project},
		fieldValue{name: "provider_id", value: s.ProviderID},
	)
}

// PolicyStore keeps validated, immutable policy snapshots by exact project
// and provider scope. Missing policies fail closed with ErrPolicyNotFound.
type PolicyStore struct {
	mu        sync.RWMutex
	persistMu sync.Mutex
	policies  map[PolicyScope]Policy
	path      string
}

func NewPolicyStore() *PolicyStore {
	return &PolicyStore{policies: make(map[PolicyScope]Policy)}
}

// Save validates and snapshots a policy before replacing its scoped entry.
func (s *PolicyStore) Save(scope PolicyScope, policy Policy) error {
	if s == nil {
		return ErrPolicyStoreRequired
	}
	if err := scope.Validate(); err != nil {
		return err
	}
	snapshot := clonePolicy(policy)
	if err := snapshot.Validate(); err != nil {
		return fmt.Errorf("validate tool route policy: %w", err)
	}
	for i, rule := range snapshot.Rules {
		if rule.Project != scope.Project || rule.ProviderID != scope.ProviderID {
			return fmt.Errorf("%w: rule[%d] targets project %q and provider %q", ErrPolicyScopeMismatch, i, rule.Project, rule.ProviderID)
		}
	}

	// Serialize writers while allowing readers to keep using the last durable
	// snapshot during filesystem I/O.
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	next := s.snapshotPolicies()
	if s.path != "" {
		if err := persistPolicyStoreLocked(s.path, scope, snapshot); err != nil {
			return fmt.Errorf("persist tool route policies: %w", err)
		}
		loaded, err := loadPolicyStore(s.path)
		if err != nil {
			return fmt.Errorf("reload tool route policies: %w", err)
		}
		next = loaded
	} else {
		next[scope] = snapshot
	}
	s.mu.Lock()
	s.policies = next
	s.mu.Unlock()
	return nil
}

func (s *PolicyStore) snapshotPolicies() map[PolicyScope]Policy {
	s.mu.RLock()
	defer s.mu.RUnlock()

	next := make(map[PolicyScope]Policy, len(s.policies)+1)
	for storedScope, storedPolicy := range s.policies {
		next[storedScope] = storedPolicy
	}
	return next
}

// Get returns a detached policy snapshot for one exact project/provider scope.
func (s *PolicyStore) Get(scope PolicyScope) (Policy, error) {
	if s == nil {
		return Policy{}, ErrPolicyStoreRequired
	}
	if err := scope.Validate(); err != nil {
		return Policy{}, err
	}

	s.mu.RLock()
	policy, ok := s.policies[scope]
	s.mu.RUnlock()
	if !ok {
		return Policy{}, ErrPolicyNotFound
	}
	return clonePolicy(policy), nil
}
