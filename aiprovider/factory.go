package aiprovider

import "fmt"

const (
	ProviderAnthropic = "anthropic"
	ProviderOpenAI    = "openai"
	ProviderGemini    = "gemini"
	ProviderMistral   = "mistral"
)

// ProviderNames lists the valid provider ids in Settings' cycle order.
// Anthropic first: it is this account's primary provider.
var ProviderNames = []string{ProviderAnthropic, ProviderOpenAI, ProviderGemini, ProviderMistral}

// DisplayName returns a human label for a provider id, for Settings and
// error messages. Unrecognized ids (including "") read as Anthropic, the
// default — matching New's fallback below.
func DisplayName(providerName string) string {
	switch providerName {
	case ProviderOpenAI:
		return "OpenAI"
	case ProviderGemini:
		return "Gemini"
	case ProviderMistral:
		return "Mistral"
	default:
		return "Anthropic"
	}
}

// Keys holds the per-provider API keys configured in Settings. The zero
// value (all empty) is valid — it just means nothing is configured yet, and
// every call fails closed with ErrNoAPIKey rather than silently no-op-ing.
type Keys struct {
	Anthropic string
	OpenAI    string
	Gemini    string
	Mistral   string
}

// New returns the TextProvider for providerName (summarizing and extracting
// action items), using the matching key from keys. An empty providerName
// defaults to Anthropic. The returned error always wraps ErrNoAPIKey when the
// cause is a missing key, so callers can match on that rather than parsing
// text.
func New(providerName string, keys Keys) (TextProvider, error) {
	switch providerName {
	case ProviderOpenAI:
		if keys.OpenAI == "" {
			return nil, fmt.Errorf("%w for OpenAI", ErrNoAPIKey)
		}
		return NewOpenAI(keys.OpenAI), nil
	case ProviderGemini:
		if keys.Gemini == "" {
			return nil, fmt.Errorf("%w for Gemini", ErrNoAPIKey)
		}
		return NewGemini(keys.Gemini), nil
	case ProviderMistral:
		if keys.Mistral == "" {
			return nil, fmt.Errorf("%w for Mistral", ErrNoAPIKey)
		}
		return NewMistral(keys.Mistral), nil
	case ProviderAnthropic, "":
		if keys.Anthropic == "" {
			return nil, fmt.Errorf("%w for Anthropic", ErrNoAPIKey)
		}
		return NewAnthropic(keys.Anthropic), nil
	default:
		return nil, fmt.Errorf("unknown AI provider %q", providerName)
	}
}

// NewTranscriber returns the audio transcription provider. Always OpenAI,
// regardless of which TextProvider is configured as the main one — see
// TranscriptionProvider's doc comment on why Anthropic/Gemini/Mistral can't
// serve this regardless of their key being set.
func NewTranscriber(keys Keys) (TranscriptionProvider, error) {
	if keys.OpenAI == "" {
		return nil, fmt.Errorf("transcription needs an OpenAI API key (Anthropic and Gemini have no speech-to-text endpoint): %w", ErrNoAPIKey)
	}
	return NewOpenAI(keys.OpenAI), nil
}
