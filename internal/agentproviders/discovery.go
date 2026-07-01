package agentproviders

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"time"
)

const localEndpointProbeTimeout = 350 * time.Millisecond

const (
	StateAvailable          = "available"
	StateNotInstalled       = "not_installed"
	CredentialExternalLogin = "external_login"
	CredentialNone          = "none"
	VersionUnknown          = "unknown"
)

type LookupFunc func(file string) (string, error)
type EndpointProbeFunc func(probeURL string) bool

type EndpointCandidate struct {
	Entrypoint string
	ProbeURL   string
}

type LocalProviderDefinition struct {
	ID             string
	Name           string
	Category       string
	Entrypoint     string
	Candidates     []string
	Version        string
	Capabilities   []string
	CredentialMode string
	AuthHint       string
	InstallHint    string
	Endpoints      []EndpointCandidate
}

type LocalProviderStatus struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Category       string   `json:"category"`
	State          string   `json:"state"`
	Installed      bool     `json:"installed"`
	Command        string   `json:"command,omitempty"`
	Entrypoint     string   `json:"entrypoint"`
	Candidates     []string `json:"candidates"`
	Version        string   `json:"version"`
	Capabilities   []string `json:"capabilities"`
	CredentialMode string   `json:"credential_mode"`
	AuthHint       string   `json:"auth_hint"`
	InstallHint    string   `json:"install_hint,omitempty"`
}

type Discoverer struct {
	lookup LookupFunc
	probe  EndpointProbeFunc
}

type Option func(*Discoverer)

func WithLookupFunc(lookup LookupFunc) Option {
	return func(d *Discoverer) {
		if lookup != nil {
			d.lookup = lookup
		}
	}
}

func WithEndpointProbeFunc(probe EndpointProbeFunc) Option {
	return func(d *Discoverer) {
		if probe != nil {
			d.probe = probe
		}
	}
}

func NewDiscoverer(opts ...Option) Discoverer {
	d := Discoverer{lookup: exec.LookPath, probe: defaultEndpointProbe}
	for _, opt := range opts {
		opt(&d)
	}
	return d
}

func DiscoverLocal() []LocalProviderStatus {
	return NewDiscoverer().DiscoverLocal()
}

func (d Discoverer) DiscoverLocal() []LocalProviderStatus {
	definitions := DefaultLocalProviders()
	statuses := make([]LocalProviderStatus, 0, len(definitions))
	for _, definition := range definitions {
		statuses = append(statuses, d.status(definition))
	}
	return statuses
}

func (d Discoverer) status(definition LocalProviderDefinition) LocalProviderStatus {
	credentialMode := definition.CredentialMode
	if credentialMode == "" {
		credentialMode = CredentialExternalLogin
	}
	version := definition.Version
	if version == "" {
		version = VersionUnknown
	}
	status := LocalProviderStatus{
		ID:             definition.ID,
		Name:           definition.Name,
		Category:       definition.Category,
		State:          StateNotInstalled,
		Entrypoint:     definition.Entrypoint,
		Candidates:     cloneStringSlice(definition.Candidates),
		Version:        version,
		Capabilities:   cloneStringSlice(definition.Capabilities),
		CredentialMode: credentialMode,
		AuthHint:       definition.AuthHint,
		InstallHint:    definition.InstallHint,
	}
	for _, command := range definition.Candidates {
		if _, err := d.lookup(command); err == nil {
			status.Installed = true
			status.State = StateAvailable
			status.Command = command
			status.InstallHint = ""
			break
		}
	}
	if !status.Installed {
		for _, endpoint := range definition.Endpoints {
			if !d.probe(endpoint.ProbeURL) {
				continue
			}
			status.Installed = true
			status.State = StateAvailable
			status.Entrypoint = endpoint.Entrypoint
			status.InstallHint = ""
			break
		}
	}
	return status
}

func defaultEndpointProbe(probeURL string) bool {
	parsed, err := url.Parse(probeURL)
	if err != nil ||
		parsed.Scheme != "http" ||
		parsed.User != nil ||
		parsed.Path != "/v1/models" ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" ||
		!isLoopbackHost(parsed.Hostname()) {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), localEndpointProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return false
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{
		Timeout: localEndpointProbeTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Jar: nil,
		Transport: &http.Transport{
			DisableCompression: true,
			DisableKeepAlives:  true,
			Proxy:              nil,
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices
}

func isLoopbackHost(host string) bool {
	return isLoopbackHostWithLookup(host, net.LookupIP)
}

func isLoopbackHostWithLookup(host string, lookup func(string) ([]net.IP, error)) bool {
	ip := net.ParseIP(host)
	if ip != nil {
		return ip.IsLoopback()
	}
	ips, err := lookup(host)
	if err != nil {
		return false
	}
	for _, resolved := range ips {
		if !resolved.IsLoopback() {
			return false
		}
	}
	return len(ips) > 0
}

func cloneStringSlice(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	return append([]string(nil), values...)
}

func DefaultLocalProviders() []LocalProviderDefinition {
	return []LocalProviderDefinition{
		{
			ID:         "codex",
			Name:       "Codex CLI",
			Category:   "local_agent",
			Entrypoint: "codex",
			Candidates: []string{"codex"},
			Capabilities: []string{
				"chat",
				"code_editing",
				"iac_assistance",
				"local_cli",
			},
			AuthHint:    "Use the official local Codex sign-in; IaC Studio does not collect ChatGPT credentials.",
			InstallHint: "Install the Codex CLI and sign in locally.",
		},
		{
			ID:         "claude",
			Name:       "Claude Code CLI",
			Category:   "local_agent",
			Entrypoint: "claude",
			Candidates: []string{"claude"},
			Capabilities: []string{
				"chat",
				"code_editing",
				"iac_assistance",
				"local_cli",
			},
			AuthHint:    "Use the official local Claude Code sign-in; IaC Studio does not collect Claude credentials.",
			InstallHint: "Install Claude Code and sign in locally.",
		},
		{
			ID:         "gemini",
			Name:       "Gemini CLI",
			Category:   "local_agent",
			Entrypoint: "gemini",
			Candidates: []string{"gemini"},
			Capabilities: []string{
				"chat",
				"code_editing",
				"iac_assistance",
				"local_cli",
			},
			AuthHint:    "Use the local Gemini session when present; hosted API keys stay on the separate API path.",
			InstallHint: "Install the Gemini CLI and sign in locally.",
		},
		{
			ID:         "copilot",
			Name:       "GitHub Copilot CLI",
			Category:   "local_agent",
			Entrypoint: "gh-copilot",
			Candidates: []string{"gh-copilot"},
			Capabilities: []string{
				"chat",
				"code_assistance",
				"iac_assistance",
				"local_cli",
			},
			AuthHint:    "Use the local GitHub CLI auth session and Copilot entitlement.",
			InstallHint: "Install the GitHub CLI Copilot extension and sign in locally.",
		},
		{
			ID:         "ollama",
			Name:       "Ollama",
			Category:   "local_model",
			Entrypoint: "ollama",
			Candidates: []string{"ollama"},
			Capabilities: []string{
				"chat",
				"iac_assistance",
				"local_model",
				"offline_runtime",
			},
			CredentialMode: CredentialNone,
			AuthHint:       "Uses local models and does not require cloud credentials.",
			InstallHint:    "Install Ollama to enable local model workflows.",
		},
		{
			ID:         "openai-compatible-local",
			Name:       "LM Studio / vLLM",
			Category:   "local_model",
			Entrypoint: "OpenAI-compatible local endpoint",
			Endpoints: []EndpointCandidate{
				{Entrypoint: "http://127.0.0.1:1234/v1", ProbeURL: "http://127.0.0.1:1234/v1/models"},
				{Entrypoint: "http://127.0.0.1:8000/v1", ProbeURL: "http://127.0.0.1:8000/v1/models"},
			},
			Capabilities: []string{
				"chat",
				"iac_assistance",
				"local_model",
				"openai_compatible",
			},
			CredentialMode: CredentialNone,
			AuthHint:       "Uses a local OpenAI-compatible endpoint; IaC Studio probes /v1/models only and does not send prompts or credentials.",
			InstallHint:    "Start LM Studio, vLLM, or another local OpenAI-compatible server.",
		},
	}
}
