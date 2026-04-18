package providers

import "fmt"

// New returns the Provider implementation matching cfg.Kind. When cfg.Kind is
// empty, it's inferred from the credentials: a non-empty APIKey implies the
// OpenAI-compatible path (matching the pre-refactor bridge's behaviour), and
// the absence of a key implies Ollama. Explicit Kind values always win.
func New(cfg Config) (Provider, error) {
	kind := cfg.Kind
	if kind == "" {
		if cfg.APIKey != "" {
			kind = KindOpenAI
		} else {
			kind = KindOllama
		}
	}

	switch kind {
	case KindOllama:
		return NewOllama(cfg), nil
	case KindOpenAI:
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("openai provider requires an API key")
		}
		return NewOpenAI(cfg), nil
	case KindAnthropic:
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("anthropic provider requires an API key")
		}
		return NewAnthropic(cfg), nil
	default:
		return nil, fmt.Errorf("unknown provider kind: %q", kind)
	}
}
