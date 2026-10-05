package aiprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenAISummarize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization header = %q", got)
		}
		resp := openAIChatResponse{}
		resp.Choices = []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}{{}}
		resp.Choices[0].Message.Content = "  The summary.  "
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &OpenAIProvider{APIKey: "test-key", BaseURL: srv.URL}
	got, err := p.Summarize(context.Background(), "notes")
	if err != nil {
		t.Fatal(err)
	}
	if got != "The summary." {
		t.Fatalf("Summarize() = %q", got)
	}
}

func TestOpenAIExtractActionItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openAIChatResponse{}
		resp.Choices = []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}{{}}
		resp.Choices[0].Message.Content = `[{"title":"Follow up with legal","project":"","tags":[],"priority":"m","due":""}]`
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &OpenAIProvider{APIKey: "test-key", BaseURL: srv.URL}
	got, err := p.ExtractActionItems(context.Background(), "t", "s", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "Follow up with legal" {
		t.Fatalf("ExtractActionItems() = %+v", got)
	}
}

func TestOpenAINoAPIKey(t *testing.T) {
	p := &OpenAIProvider{}
	if _, err := p.Summarize(context.Background(), "x"); err != ErrNoAPIKey {
		t.Fatalf("err = %v, want ErrNoAPIKey", err)
	}
	if _, err := p.Transcribe(context.Background(), "x.wav"); err != ErrNoAPIKey {
		t.Fatalf("Transcribe err = %v, want ErrNoAPIKey", err)
	}
}

func TestOpenAITranscribe(t *testing.T) {
	var gotModel string
	var gotFilename string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Fatal(err)
		}
		gotModel = r.FormValue("model")
		if fh := r.MultipartForm.File["file"]; len(fh) == 1 {
			gotFilename = fh[0].Filename
		}
		json.NewEncoder(w).Encode(openAITranscriptionResponse{Text: "  hello there  "})
	}))
	defer srv.Close()

	dir := t.TempDir()
	audioPath := filepath.Join(dir, "meeting.wav")
	if err := os.WriteFile(audioPath, []byte("fake wav bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := &OpenAIProvider{APIKey: "test-key", BaseURL: srv.URL}
	got, err := p.Transcribe(context.Background(), audioPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello there" {
		t.Fatalf("Transcribe() = %q", got)
	}
	if gotModel != openAIDefaultWhisperModel {
		t.Fatalf("uploaded model = %q, want %q", gotModel, openAIDefaultWhisperModel)
	}
	if gotFilename != "meeting.wav" {
		t.Fatalf("uploaded filename = %q", gotFilename)
	}
}

func TestOpenAITranscribeMissingFile(t *testing.T) {
	p := &OpenAIProvider{APIKey: "test-key"}
	if _, err := p.Transcribe(context.Background(), "/does/not/exist.wav"); err == nil {
		t.Fatal("expected an error for a missing audio file")
	}
}

func TestOpenAIAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		resp := openAIChatResponse{}
		resp.Error = &struct {
			Message string `json:"message"`
		}{Message: "rate limited"}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &OpenAIProvider{APIKey: "test-key", BaseURL: srv.URL}
	_, err := p.Summarize(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("err = %v, want it to mention rate limited", err)
	}
}
