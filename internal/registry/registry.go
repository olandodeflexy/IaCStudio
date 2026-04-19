// Package registry is a minimal client for the public Terraform Registry
// (https://registry.terraform.io) — enough to look up a module by address,
// list its available versions, and search by free-text query.
//
// The client is deliberately small: we don't need auth, caching, or the
// full provider-registry surface. Callers want three things, which map to
// three methods on Client:
//
//   - Get(ns, name, provider)    → latest version + declared inputs/outputs
//   - Versions(ns, name, prov)   → list of semver strings, newest first
//   - Search(query, limit)       → free-text discovery for the UI
//
// All network calls respect the caller's context for cancellation and
// timeouts. A custom BaseURL is supported so tests can point at a
// httptest server and CI can proxy through an internal mirror if needed.
package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBaseURL is the public Terraform Registry v1 endpoint. Override via
// Config.BaseURL to point at a private mirror or an httptest server.
const DefaultBaseURL = "https://registry.terraform.io/v1"

// Config configures a Client. Empty values fall back to sane defaults.
type Config struct {
	BaseURL    string
	HTTPClient *http.Client
	// UserAgent is sent with every request. The registry is tolerant about
	// this but we identify ourselves so abuse / rate-limit diagnostics have
	// something to grep for.
	UserAgent string
}

// Client talks to the Terraform Registry. Zero value is not usable — use New.
type Client struct {
	baseURL    string
	httpClient *http.Client
	userAgent  string
}

// New constructs a Client. Empty fields get sensible defaults:
//   - BaseURL:    DefaultBaseURL
//   - HTTPClient: 15-second timeout (registry is usually sub-second)
//   - UserAgent:  "iac-studio"
func New(cfg Config) *Client {
	c := &Client{
		baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
		httpClient: cfg.HTTPClient,
		userAgent:  cfg.UserAgent,
	}
	if c.baseURL == "" {
		c.baseURL = DefaultBaseURL
	}
	if c.httpClient == nil {
		c.httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	if c.userAgent == "" {
		c.userAgent = "iac-studio"
	}
	return c
}

// Module is the subset of /v1/modules/<ns>/<name>/<provider> we actually
// render. The Registry emits many more fields; we decode only those the UI
// and the module-registry API consumer need.
type Module struct {
	ID          string   `json:"id"`          // e.g., "terraform-aws-modules/vpc/aws/5.0.0"
	Namespace   string   `json:"namespace"`
	Name        string   `json:"name"`
	Provider    string   `json:"provider"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Source      string   `json:"source"`      // upstream git URL
	PublishedAt string   `json:"published_at"`
	Downloads   int      `json:"downloads"`
	Verified    bool     `json:"verified"`
	Root        ModuleRoot `json:"root"`
}

// ModuleRoot mirrors the registry's root.{inputs,outputs} so callers can
// render a form for the module's declared interface without also running
// InspectLocalModule on a fetched copy.
type ModuleRoot struct {
	Inputs  []RegistryInput  `json:"inputs"`
	Outputs []RegistryOutput `json:"outputs"`
}

type RegistryInput struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Default     string `json:"default"`    // Registry stringifies defaults regardless of source type
	Required    bool   `json:"required"`
}

type RegistryOutput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// SearchResult wraps the /v1/modules/search endpoint's response. Modules
// is truncated to the caller's limit.
type SearchResult struct {
	Modules []Module `json:"modules"`
}

// Get fetches the latest version of the module at <namespace>/<name>/<provider>.
// Registry URL form: /v1/modules/<ns>/<name>/<provider> (no version → latest).
func (c *Client) Get(ctx context.Context, namespace, name, provider string) (*Module, error) {
	if namespace == "" || name == "" || provider == "" {
		return nil, fmt.Errorf("namespace, name, provider are required")
	}
	path := fmt.Sprintf("/modules/%s/%s/%s",
		url.PathEscape(namespace),
		url.PathEscape(name),
		url.PathEscape(provider),
	)
	var mod Module
	if err := c.do(ctx, path, &mod); err != nil {
		return nil, err
	}
	return &mod, nil
}

// Versions returns the list of published versions for a module, newest
// first. Not all callers need the full Module object — version listings
// drive the "pin by version" UI.
type versionsResponse struct {
	Modules []struct {
		Versions []struct {
			Version string `json:"version"`
		} `json:"versions"`
	} `json:"modules"`
}

// Versions returns the semver strings published for a module. The registry
// sorts newest-first already; we preserve that order.
func (c *Client) Versions(ctx context.Context, namespace, name, provider string) ([]string, error) {
	path := fmt.Sprintf("/modules/%s/%s/%s/versions",
		url.PathEscape(namespace),
		url.PathEscape(name),
		url.PathEscape(provider),
	)
	var resp versionsResponse
	if err := c.do(ctx, path, &resp); err != nil {
		return nil, err
	}
	if len(resp.Modules) == 0 {
		return nil, nil
	}
	versions := make([]string, 0, len(resp.Modules[0].Versions))
	for _, v := range resp.Modules[0].Versions {
		versions = append(versions, v.Version)
	}
	return versions, nil
}

// Search runs a free-text query against the registry. limit caps results;
// the registry allows up to 100 but a UI list of 20-40 is plenty.
func (c *Client) Search(ctx context.Context, query string, limit int) (*SearchResult, error) {
	if strings.TrimSpace(query) == "" {
		return &SearchResult{}, nil
	}
	if limit <= 0 {
		limit = 20
	}
	q := url.Values{}
	q.Set("q", query)
	q.Set("limit", fmt.Sprintf("%d", limit))
	var out SearchResult
	if err := c.do(ctx, "/modules/search?"+q.Encode(), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// do is the shared HTTP call path: build the request, decode JSON, surface
// non-2xx status codes with the body appended so users see the registry's
// actual error message rather than a bare status line.
func (c *Client) do(ctx context.Context, path string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("registry request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Read the body as text so even HTML error pages (rare but
		// possible on outages) surface in the error message.
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("registry: %s %s → %s: %s",
			req.Method, req.URL.Path, resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("registry decode: %w", err)
	}
	return nil
}
