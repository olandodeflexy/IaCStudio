package agentproviders

import "os/exec"

const (
	StateAvailable          = "available"
	StateNotInstalled       = "not_installed"
	CredentialExternalLogin = "external_login"
	CredentialNone          = "none"
)

type LookupFunc func(file string) (string, error)

type LocalProviderDefinition struct {
	ID             string
	Name           string
	Category       string
	Entrypoint     string
	Candidates     []string
	CredentialMode string
	AuthHint       string
	InstallHint    string
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
	CredentialMode string   `json:"credential_mode"`
	AuthHint       string   `json:"auth_hint"`
	InstallHint    string   `json:"install_hint,omitempty"`
}

type Discoverer struct {
	lookup LookupFunc
}

type Option func(*Discoverer)

func WithLookupFunc(lookup LookupFunc) Option {
	return func(d *Discoverer) {
		if lookup != nil {
			d.lookup = lookup
		}
	}
}

func NewDiscoverer(opts ...Option) Discoverer {
	d := Discoverer{lookup: exec.LookPath}
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
	status := LocalProviderStatus{
		ID:             definition.ID,
		Name:           definition.Name,
		Category:       definition.Category,
		State:          StateNotInstalled,
		Entrypoint:     definition.Entrypoint,
		Candidates:     append([]string(nil), definition.Candidates...),
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
	return status
}

func DefaultLocalProviders() []LocalProviderDefinition {
	return []LocalProviderDefinition{
		{
			ID:          "codex",
			Name:        "Codex CLI",
			Category:    "local_agent",
			Entrypoint:  "codex",
			Candidates:  []string{"codex"},
			AuthHint:    "Use the official local Codex sign-in; IaC Studio does not collect ChatGPT credentials.",
			InstallHint: "Install the Codex CLI and sign in locally.",
		},
		{
			ID:          "claude",
			Name:        "Claude Code CLI",
			Category:    "local_agent",
			Entrypoint:  "claude",
			Candidates:  []string{"claude"},
			AuthHint:    "Use the official local Claude Code sign-in; IaC Studio does not collect Claude credentials.",
			InstallHint: "Install Claude Code and sign in locally.",
		},
		{
			ID:          "gemini",
			Name:        "Gemini CLI",
			Category:    "local_agent",
			Entrypoint:  "gemini",
			Candidates:  []string{"gemini"},
			AuthHint:    "Use the local Gemini session when present; hosted API keys stay on the separate API path.",
			InstallHint: "Install the Gemini CLI and sign in locally.",
		},
		{
			ID:          "copilot",
			Name:        "GitHub Copilot CLI",
			Category:    "local_agent",
			Entrypoint:  "gh copilot",
			Candidates:  []string{"gh-copilot"},
			AuthHint:    "Use the local GitHub CLI auth session and Copilot entitlement.",
			InstallHint: "Install the GitHub CLI Copilot extension and sign in locally.",
		},
		{
			ID:             "ollama",
			Name:           "Ollama",
			Category:       "local_model",
			Entrypoint:     "ollama",
			Candidates:     []string{"ollama"},
			CredentialMode: CredentialNone,
			AuthHint:       "Uses local models and does not require cloud credentials.",
			InstallHint:    "Install Ollama to enable local model workflows.",
		},
	}
}
