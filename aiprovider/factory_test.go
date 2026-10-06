package aiprovider

import (
	"errors"
	"testing"
)

func TestNewDefaultsToAnthropic(t *testing.T) {
	p, err := New("", Keys{Anthropic: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "Anthropic" {
		t.Fatalf("Name() = %q, want Anthropic", p.Name())
	}
}

func TestNewEachProvider(t *testing.T) {
	cases := []struct {
		name string
		keys Keys
		want string
	}{
		{ProviderAnthropic, Keys{Anthropic: "k"}, "Anthropic"},
		{ProviderOpenAI, Keys{OpenAI: "k"}, "OpenAI"},
		{ProviderGemini, Keys{Gemini: "k"}, "Gemini"},
		{ProviderMistral, Keys{Mistral: "k"}, "Mistral"},
	}
	for _, c := range cases {
		p, err := New(c.name, c.keys)
		if err != nil {
			t.Fatalf("New(%q): %v", c.name, err)
		}
		if p.Name() != c.want {
			t.Fatalf("New(%q).Name() = %q, want %q", c.name, p.Name(), c.want)
		}
	}
}

func TestNewMissingKey(t *testing.T) {
	if _, err := New(ProviderOpenAI, Keys{}); !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("err = %v, want to wrap ErrNoAPIKey", err)
	}
}

func TestNewUnknownProvider(t *testing.T) {
	if _, err := New("not-a-real-provider", Keys{}); err == nil {
		t.Fatal("expected an error for an unknown provider")
	}
}

func TestNewTranscriberRequiresOpenAIRegardlessOfTextProvider(t *testing.T) {
	// Anthropic configured as the text provider, but no OpenAI key: transcription
	// must still fail, since Anthropic has no speech-to-text endpoint.
	if _, err := NewTranscriber(Keys{Anthropic: "k"}); !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("err = %v, want ErrNoAPIKey", err)
	}
	tr, err := NewTranscriber(Keys{Anthropic: "k", OpenAI: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Name() != "OpenAI" {
		t.Fatalf("Name() = %q, want OpenAI", tr.Name())
	}
}

func TestDisplayName(t *testing.T) {
	cases := map[string]string{
		ProviderAnthropic: "Anthropic",
		ProviderOpenAI:    "OpenAI",
		ProviderGemini:    "Gemini",
		ProviderMistral:   "Mistral",
		"":                "Anthropic",
		"garbage":         "Anthropic",
	}
	for in, want := range cases {
		if got := DisplayName(in); got != want {
			t.Errorf("DisplayName(%q) = %q, want %q", in, got, want)
		}
	}
}
