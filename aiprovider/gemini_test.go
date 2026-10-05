package aiprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGeminiSummarize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("key"); got != "test-key" {
			t.Errorf("key query param = %q, want test-key", got)
		}
		if !strings.Contains(r.URL.Path, geminiDefaultModel) {
			t.Errorf("request path %q does not mention model %q", r.URL.Path, geminiDefaultModel)
		}
		var resp geminiResponse
		resp.Candidates = []struct {
			Content geminiContent `json:"content"`
		}{{Content: geminiContent{Parts: []geminiPart{{Text: "  Summary text.  "}}}}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &GeminiProvider{APIKey: "test-key", BaseURL: srv.URL}
	got, err := p.Summarize(context.Background(), "notes")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Summary text." {
		t.Fatalf("Summarize() = %q", got)
	}
}

func TestGeminiExtractActionItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var resp geminiResponse
		resp.Candidates = []struct {
			Content geminiContent `json:"content"`
		}{{Content: geminiContent{Parts: []geminiPart{{Text: `[{"title":"Book the venue","project":"","tags":["events"],"priority":"l","due":""}]`}}}}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &GeminiProvider{APIKey: "test-key", BaseURL: srv.URL}
	got, err := p.ExtractActionItems(context.Background(), "t", "s", nil, []string{"events"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "Book the venue" || got[0].Priority != "l" {
		t.Fatalf("ExtractActionItems() = %+v", got)
	}
}

func TestGeminiNoAPIKey(t *testing.T) {
	p := &GeminiProvider{}
	if _, err := p.Summarize(context.Background(), "x"); err != ErrNoAPIKey {
		t.Fatalf("err = %v, want ErrNoAPIKey", err)
	}
}

func TestGeminiAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		var resp geminiResponse
		resp.Error = &struct {
			Message string `json:"message"`
		}{Message: "invalid API key"}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &GeminiProvider{APIKey: "bad", BaseURL: srv.URL}
	_, err := p.Summarize(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "invalid API key") {
		t.Fatalf("err = %v", err)
	}
}
