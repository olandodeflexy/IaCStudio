package mcpairlock

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/iac-studio/iac-studio/internal/cloudconnections"
)

const (
	// MaxCredentialSessionTTL bounds how long a broker may make credentials
	// available for one session. A later broker may issue a shorter profile.
	MaxCredentialSessionTTL = 15 * time.Minute

	maxCredentialSessionIdentifierBytes = 128
	maxCredentialSessionServerCount     = 16
	maxCredentialSessionScopeBytes      = 128
)

var ErrInvalidCredentialSessionProfile = errors.New("invalid MCP credential session profile")

// CredentialSessionScope contains display-safe cloud identifiers. Callers must
// derive these values from public Cloud Connection metadata, never secrets.
type CredentialSessionScope struct {
	Region         string `json:"region,omitempty"`
	AccountID      string `json:"account_id,omitempty"`
	SubscriptionID string `json:"subscription_id,omitempty"`
	CloudProjectID string `json:"cloud_project_id,omitempty"`
}

// CredentialSessionProfile is the secret-free authorization envelope for one
// future external MCP credential session. It intentionally defines no fields
// for raw credentials, secret references, environment variables, or arguments.
type CredentialSessionProfile struct {
	ID               string                 `json:"id"`
	ProjectID        string                 `json:"project_id"`
	Provider         string                 `json:"provider"`
	ConnectionID     string                 `json:"connection_id"`
	Scope            CredentialSessionScope `json:"scope"`
	AllowedServerIDs []string               `json:"allowed_server_ids"`
	IssuedAt         time.Time              `json:"issued_at"`
	ExpiresAt        time.Time              `json:"expires_at"`
}

// Validate checks the profile without resolving a Cloud Connection or
// launching an external process.
func (p CredentialSessionProfile) Validate(now time.Time) error {
	if now.IsZero() {
		return credentialSessionProfileError("validation time is required")
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "id", value: p.ID},
		{name: "project_id", value: p.ProjectID},
		{name: "provider", value: p.Provider},
		{name: "connection_id", value: p.ConnectionID},
	} {
		if err := validateCredentialSessionIdentifier(field.name, field.value); err != nil {
			return err
		}
	}
	if p.Provider != strings.ToLower(p.Provider) {
		return credentialSessionProfileError("provider must be lowercase")
	}
	if err := p.Scope.validate(p.Provider); err != nil {
		return err
	}
	if len(p.AllowedServerIDs) == 0 {
		return credentialSessionProfileError("at least one allowed server is required")
	}
	if len(p.AllowedServerIDs) > maxCredentialSessionServerCount {
		return credentialSessionProfileError("allowed server count exceeds %d", maxCredentialSessionServerCount)
	}
	seen := make(map[string]struct{}, len(p.AllowedServerIDs))
	for _, id := range p.AllowedServerIDs {
		if err := validateCredentialSessionIdentifier("allowed_server_id", id); err != nil {
			return err
		}
		if _, exists := seen[id]; exists {
			return credentialSessionProfileError("allowed server ids must be unique")
		}
		seen[id] = struct{}{}
	}
	if p.IssuedAt.IsZero() {
		return credentialSessionProfileError("issued_at is required")
	}
	if p.ExpiresAt.IsZero() {
		return credentialSessionProfileError("expires_at is required")
	}
	if p.IssuedAt.After(now) {
		return credentialSessionProfileError("issued_at must not be in the future")
	}
	if !p.ExpiresAt.After(now) {
		return credentialSessionProfileError("profile is expired")
	}
	ttl := p.ExpiresAt.Sub(p.IssuedAt)
	if ttl <= 0 {
		return credentialSessionProfileError("expires_at must be after issued_at")
	}
	if ttl > MaxCredentialSessionTTL {
		return credentialSessionProfileError("profile lifetime exceeds %s", MaxCredentialSessionTTL)
	}
	return nil
}

// AllowsServer reports whether the exact registry server identity is in scope.
func (p CredentialSessionProfile) AllowsServer(id string) bool {
	for _, allowed := range p.AllowedServerIDs {
		if allowed == id {
			return true
		}
	}
	return false
}

func (s CredentialSessionScope) validate(provider string) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "region", value: s.Region},
		{name: "account_id", value: s.AccountID},
		{name: "subscription_id", value: s.SubscriptionID},
		{name: "cloud_project_id", value: s.CloudProjectID},
	} {
		if err := validateCredentialSessionScopeValue(field.name, field.value); err != nil {
			return err
		}
	}
	identities := 0
	for _, value := range []string{s.AccountID, s.SubscriptionID, s.CloudProjectID} {
		if value != "" {
			identities++
		}
	}
	if identities > 1 {
		return credentialSessionProfileError("cloud scope must contain at most one account, subscription, or cloud project id")
	}
	switch provider {
	case cloudconnections.ProviderAWS:
		if s.SubscriptionID != "" || s.CloudProjectID != "" {
			return credentialSessionProfileError("AWS scope may only contain account_id")
		}
		if s.AccountID == "" || s.Region == "" {
			return credentialSessionProfileError("AWS scope requires account_id and region")
		}
	case cloudconnections.ProviderAzure:
		if s.AccountID != "" || s.CloudProjectID != "" {
			return credentialSessionProfileError("Azure scope may only contain subscription_id")
		}
		if s.SubscriptionID == "" {
			return credentialSessionProfileError("Azure scope requires subscription_id")
		}
	case cloudconnections.ProviderGCP:
		if s.AccountID != "" || s.SubscriptionID != "" {
			return credentialSessionProfileError("GCP scope may only contain cloud_project_id")
		}
		if s.CloudProjectID == "" {
			return credentialSessionProfileError("GCP scope requires cloud_project_id")
		}
	default:
		return credentialSessionProfileError("provider is not supported by Cloud Connections")
	}
	return nil
}

func validateCredentialSessionIdentifier(name, value string) error {
	if value == "" {
		return credentialSessionProfileError("%s is required", name)
	}
	if strings.TrimSpace(value) != value {
		return credentialSessionProfileError("%s must not contain leading or trailing whitespace", name)
	}
	if len(value) > maxCredentialSessionIdentifierBytes {
		return credentialSessionProfileError("%s exceeds %d bytes", name, maxCredentialSessionIdentifierBytes)
	}
	for index := 0; index < len(value); index++ {
		char := value[index]
		if isASCIIAlpha(char) || (char >= '0' && char <= '9') || char == '-' || char == '_' {
			continue
		}
		return credentialSessionProfileError("%s contains a character outside the ASCII identifier allowlist", name)
	}
	return nil
}

func validateCredentialSessionScopeValue(name, value string) error {
	if value == "" {
		return nil
	}
	if strings.TrimSpace(value) != value {
		return credentialSessionProfileError("scope %s must not contain leading or trailing whitespace", name)
	}
	if len(value) > maxCredentialSessionScopeBytes {
		return credentialSessionProfileError("scope %s exceeds %d bytes", name, maxCredentialSessionScopeBytes)
	}
	for index := 0; index < len(value); index++ {
		char := value[index]
		if isASCIIAlpha(char) || (char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' {
			continue
		}
		return credentialSessionProfileError("scope %s contains a character outside the ASCII scope allowlist", name)
	}
	return nil
}

func credentialSessionProfileError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidCredentialSessionProfile, fmt.Sprintf(format, args...))
}
