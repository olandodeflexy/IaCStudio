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
		// Added in a later commit; surface a clear error until then so the
		// router can tell the user the feature is on the way rather than
		// silently falling back to another provider.
		return nil, fmt.Errorf("anthropic provider not implemented yet")
	default:
		return nil, fmt.Errorf("unknown provider kind: %q", kind)
	}
}
