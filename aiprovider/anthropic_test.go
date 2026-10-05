package aiprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAnthropicSummarize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Errorf("x-api-key header = %q, want test-key", got)
		}
		if got := r.Header.Get("anthropic-version"); got == "" {
			t.Error("missing anthropic-version header")
		}
		var req anthropicRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if len(req.Messages) != 1 || req.Messages[0].Role != "user" {
			t.Fatalf("request messages = %+v", req.Messages)
		}
		json.NewEncoder(w).Encode(anthropicResponse{
			Content: []anthropicContentBlock{{Type: "text", Text: "  A short summary.  "}},
		})
	}))
	defer srv.Close()

	p := &AnthropicProvider{APIKey: "test-key", BaseURL: srv.URL}
	got, err := p.Summarize(context.Background(), "we discussed the roadmap")
	if err != nil {
		t.Fatal(err)
	}
	if got != "A short summary." {
		t.Fatalf("Summarize() = %q", got)
	}
}

func TestAnthropicExtractActionItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(anthropicResponse{
			Content: []anthropicContentBlock{{Type: "text", Text: `[{"title":"Send the invoice","project":"Billing","tags":["finance"],"priority":"h","due":"friday"}]`}},
		})
	}))
	defer srv.Close()

	p := &AnthropicProvider{APIKey: "test-key", BaseURL: srv.URL}
	got, err := p.ExtractActionItems(context.Background(), "transcript", "summary", []string{"Billing"}, []string{"finance"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "Send the invoice" || got[0].Project != "Billing" {
		t.Fatalf("ExtractActionItems() = %+v", got)
	}
}

func TestAnthropicNoAPIKey(t *testing.T) {
	p := &AnthropicProvider{}
	if _, err := p.Summarize(context.Background(), "x"); err != ErrNoAPIKey {
		t.Fatalf("err = %v, want ErrNoAPIKey", err)
	}
}

func TestAnthropicAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(anthropicResponse{Error: &anthropicAPIError{Message: "invalid x-api-key"}})
	}))
	defer srv.Close()

	p := &AnthropicProvider{APIKey: "bad-key", BaseURL: srv.URL}
	_, err := p.Summarize(context.Background(), "x")
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestAnthropicTranscriptTooLong(t *testing.T) {
	p := &AnthropicProvider{APIKey: "test-key"}
	huge := make([]byte, maxTranscriptChars+1)
	for i := range huge {
		huge[i] = 'a'
	}
	if _, err := p.Summarize(context.Background(), string(huge)); err == nil {
		t.Fatal("expected an error for an over-length transcript")
	}
}

func TestAnthropicName(t *testing.T) {
	if (&AnthropicProvider{}).Name() != "Anthropic" {
		t.Fatal("Name() mismatch")
	}
}
