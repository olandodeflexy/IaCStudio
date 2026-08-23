package mcpairlock

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCredentialSessionProfileValidatesSecretFreeScope(t *testing.T) {
	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	profile := validCredentialSessionProfile(now)

	if err := profile.Validate(now); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !profile.AllowsServer("aws-official") || profile.AllowsServer("unknown") {
		t.Fatalf("unexpected server scope: %+v", profile.AllowedServerIDs)
	}

	encoded, err := json.Marshal(profile)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, forbidden := range []string{`"secrets"`, `"secret_refs"`, `"environment"`, `"args"`} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("profile JSON contains forbidden credential-bearing field %q: %s", forbidden, encoded)
		}
	}
}

func TestCredentialSessionProfileAcceptsProviderSpecificScopes(t *testing.T) {
	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		provider string
		scope    CredentialSessionScope
	}{
		{provider: "aws", scope: CredentialSessionScope{Region: "us-east-1", AccountID: "123456789012"}},
		{provider: "azure", scope: CredentialSessionScope{Region: "eastus", SubscriptionID: "11111111-2222-3333-4444-555555555555"}},
		{provider: "gcp", scope: CredentialSessionScope{Region: "us-central1", CloudProjectID: "platform-prod"}},
	}

	for _, test := range tests {
		t.Run(test.provider, func(t *testing.T) {
			profile := validCredentialSessionProfile(now)
			profile.Provider = test.provider
			profile.Scope = test.scope
			if err := profile.Validate(now); err != nil {
				t.Fatalf("Validate: %v", err)
			}
		})
	}
}

func TestCredentialSessionProfileRejectsInvalidIdentityAndServerScope(t *testing.T) {
	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*CredentialSessionProfile)
	}{
		{name: "missing id", mutate: func(profile *CredentialSessionProfile) { profile.ID = "" }},
		{name: "project traversal", mutate: func(profile *CredentialSessionProfile) { profile.ProjectID = "../prod" }},
		{name: "uppercase provider", mutate: func(profile *CredentialSessionProfile) { profile.Provider = "AWS" }},
		{name: "unsupported provider", mutate: func(profile *CredentialSessionProfile) { profile.Provider = "other" }},
		{name: "connection whitespace", mutate: func(profile *CredentialSessionProfile) { profile.ConnectionID = " conn_1" }},
		{name: "missing servers", mutate: func(profile *CredentialSessionProfile) { profile.AllowedServerIDs = nil }},
		{name: "duplicate servers", mutate: func(profile *CredentialSessionProfile) {
			profile.AllowedServerIDs = []string{"aws-official", "aws-official"}
		}},
		{name: "server wildcard", mutate: func(profile *CredentialSessionProfile) { profile.AllowedServerIDs = []string{"*"} }},
		{name: "too many servers", mutate: func(profile *CredentialSessionProfile) {
			profile.AllowedServerIDs = make([]string, maxCredentialSessionServerCount+1)
			for index := range profile.AllowedServerIDs {
				profile.AllowedServerIDs[index] = "server_" + string(rune('a'+index))
			}
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profile := validCredentialSessionProfile(now)
			test.mutate(&profile)
			if err := profile.Validate(now); !errors.Is(err, ErrInvalidCredentialSessionProfile) {
				t.Fatalf("Validate error = %v, want ErrInvalidCredentialSessionProfile", err)
			}
		})
	}
}

func TestCredentialSessionProfileRejectsAmbiguousCloudScope(t *testing.T) {
	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		provider string
		scope    CredentialSessionScope
	}{
		{name: "AWS subscription", provider: "aws", scope: CredentialSessionScope{SubscriptionID: "sub-1"}},
		{name: "AWS missing account", provider: "aws", scope: CredentialSessionScope{Region: "us-east-1"}},
		{name: "AWS missing region", provider: "aws", scope: CredentialSessionScope{AccountID: "123456789012"}},
		{name: "Azure account", provider: "azure", scope: CredentialSessionScope{AccountID: "123456789012"}},
		{name: "Azure missing subscription", provider: "azure", scope: CredentialSessionScope{}},
		{name: "GCP account", provider: "gcp", scope: CredentialSessionScope{AccountID: "123456789012"}},
		{name: "GCP missing cloud project", provider: "gcp", scope: CredentialSessionScope{}},
		{name: "multiple identities", provider: "aws", scope: CredentialSessionScope{AccountID: "123456789012", SubscriptionID: "sub-1"}},
		{name: "control character", provider: "aws", scope: CredentialSessionScope{Region: "us-east-1\nAWS_SECRET_ACCESS_KEY"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profile := validCredentialSessionProfile(now)
			profile.Provider = test.provider
			profile.Scope = test.scope
			if err := profile.Validate(now); !errors.Is(err, ErrInvalidCredentialSessionProfile) {
				t.Fatalf("Validate error = %v, want ErrInvalidCredentialSessionProfile", err)
			}
		})
	}
}

func TestCredentialSessionProfileEnforcesShortLifetime(t *testing.T) {
	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*CredentialSessionProfile)
	}{
		{name: "missing issued at", mutate: func(profile *CredentialSessionProfile) { profile.IssuedAt = time.Time{} }},
		{name: "missing expiry", mutate: func(profile *CredentialSessionProfile) { profile.ExpiresAt = time.Time{} }},
		{name: "future issuance", mutate: func(profile *CredentialSessionProfile) { profile.IssuedAt = now.Add(time.Second) }},
		{name: "expired", mutate: func(profile *CredentialSessionProfile) { profile.ExpiresAt = now }},
		{name: "expiry before issuance", mutate: func(profile *CredentialSessionProfile) { profile.ExpiresAt = profile.IssuedAt.Add(-time.Second) }},
		{name: "lifetime too long", mutate: func(profile *CredentialSessionProfile) {
			profile.ExpiresAt = profile.IssuedAt.Add(MaxCredentialSessionTTL + time.Second)
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profile := validCredentialSessionProfile(now)
			test.mutate(&profile)
			if err := profile.Validate(now); !errors.Is(err, ErrInvalidCredentialSessionProfile) {
				t.Fatalf("Validate error = %v, want ErrInvalidCredentialSessionProfile", err)
			}
		})
	}
}

func validCredentialSessionProfile(now time.Time) CredentialSessionProfile {
	return CredentialSessionProfile{
		ID:           "mcps_123",
		ProjectID:    "production_stack",
		Provider:     "aws",
		ConnectionID: "conn_123",
		Scope: CredentialSessionScope{
			Region:    "us-east-1",
			AccountID: "123456789012",
		},
		AllowedServerIDs: []string{"aws-official", "terraform-official"},
		IssuedAt:         now,
		ExpiresAt:        now.Add(MaxCredentialSessionTTL),
	}
}
