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
		resp := openAIChatResponse{Choices: []openAIChoice{{}}}
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
		resp := openAIChatResponse{Choices: []openAIChoice{{}}}
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

func TestOpenAIChatText(t *testing.T) {
	var gotReq openAIChatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatal(err)
		}
		resp := openAIChatResponse{Choices: []openAIChoice{{}}}
		resp.Choices[0].Message.Content = "  hi there  "
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &OpenAIProvider{APIKey: "test-key", BaseURL: srv.URL}
	got, err := p.Chat(context.Background(), "system prompt", []Turn{{Role: "user", Content: "hello"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "hi there" || len(got.ToolCalls) != 0 {
		t.Fatalf("Chat() = %+v", got)
	}
	if gotReq.Messages[0].Role != "system" || gotReq.Messages[0].Content != "system prompt" {
		t.Fatalf("system message = %+v", gotReq.Messages[0])
	}
	if gotReq.Messages[1].Role != "user" || gotReq.Messages[1].Content != "hello" {
		t.Fatalf("user message = %+v", gotReq.Messages[1])
	}
}

func TestOpenAIChatToolCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openAIChatResponse{Choices: []openAIChoice{{}}}
		tc := openAIToolCall{ID: "call_1", Type: "function"}
		tc.Function.Name = "list_tasks"
		tc.Function.Arguments = `{"project":"Work"}`
		resp.Choices[0].Message.ToolCalls = []openAIToolCall{tc}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &OpenAIProvider{APIKey: "test-key", BaseURL: srv.URL}
	tools := []ToolSpec{{Name: "list_tasks", Description: "list tasks", Parameters: map[string]any{"type": "object"}}}
	got, err := p.Chat(context.Background(), "system", []Turn{{Role: "user", Content: "what's open in Work?"}}, tools)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "" || len(got.ToolCalls) != 1 {
		t.Fatalf("Chat() = %+v", got)
	}
	call := got.ToolCalls[0]
	if call.ID != "call_1" || call.Name != "list_tasks" || call.Arguments["project"] != "Work" {
		t.Fatalf("ToolCalls[0] = %+v", call)
	}
}

// TestOpenAIChatRoundTripsToolResult drives a second Chat call carrying the
// first call's assistant tool-call Turn plus its "tool" result Turn, the
// shape chatReplyCmd (main package) builds hop by hop — this is the part of
// openAITurnsToMessages that matters: the assistant message must carry
// tool_calls back out, or OpenAI's API rejects the orphaned tool message
// that follows it.
func TestOpenAIChatRoundTripsToolResult(t *testing.T) {
	var gotReq openAIChatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatal(err)
		}
		resp := openAIChatResponse{Choices: []openAIChoice{{}}}
		resp.Choices[0].Message.Content = "You have one open task."
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	history := []Turn{
		{Role: "user", Content: "what's open?"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "list_tasks", Arguments: map[string]any{}}}},
		{Role: "tool", ToolCallID: "call_1", Content: `[{"id":"1","title":"Ship report"}]`},
	}
	p := &OpenAIProvider{APIKey: "test-key", BaseURL: srv.URL}
	got, err := p.Chat(context.Background(), "system", history, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "You have one open task." {
		t.Fatalf("Chat() = %+v", got)
	}
	// system, user, assistant(tool_calls), tool
	if len(gotReq.Messages) != 4 {
		t.Fatalf("len(Messages) = %d, want 4: %+v", len(gotReq.Messages), gotReq.Messages)
	}
	assistantMsg := gotReq.Messages[2]
	if len(assistantMsg.ToolCalls) != 1 || assistantMsg.ToolCalls[0].ID != "call_1" {
		t.Fatalf("assistant message tool_calls = %+v", assistantMsg.ToolCalls)
	}
	toolMsg := gotReq.Messages[3]
	if toolMsg.Role != "tool" || toolMsg.ToolCallID != "call_1" {
		t.Fatalf("tool message = %+v", toolMsg)
	}
}
