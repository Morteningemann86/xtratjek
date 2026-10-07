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
		_ = json.NewEncoder(w).Encode(resp)
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
		_ = json.NewEncoder(w).Encode(resp)
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
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &GeminiProvider{APIKey: "bad", BaseURL: srv.URL}
	_, err := p.Summarize(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "invalid API key") {
		t.Fatalf("err = %v", err)
	}
}

func TestGeminiChatText(t *testing.T) {
	var gotReq geminiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatal(err)
		}
		var resp geminiResponse
		resp.Candidates = []struct {
			Content geminiContent `json:"content"`
		}{{Content: geminiContent{Role: "model", Parts: []geminiPart{{Text: "hi there"}}}}}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &GeminiProvider{APIKey: "test-key", BaseURL: srv.URL}
	got, err := p.Chat(context.Background(), "system prompt", []Turn{{Role: "user", Content: "hello"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "hi there" || len(got.ToolCalls) != 0 {
		t.Fatalf("Chat() = %+v", got)
	}
	if gotReq.SystemInstruction == nil || gotReq.SystemInstruction.Parts[0].Text != "system prompt" {
		t.Fatalf("SystemInstruction = %+v", gotReq.SystemInstruction)
	}
	if len(gotReq.Contents) != 1 || gotReq.Contents[0].Role != "user" {
		t.Fatalf("Contents = %+v", gotReq.Contents)
	}
}

func TestGeminiChatFunctionCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var resp geminiResponse
		resp.Candidates = []struct {
			Content geminiContent `json:"content"`
		}{{Content: geminiContent{Role: "model", Parts: []geminiPart{
			{FunctionCall: &geminiFunctionCall{Name: "list_tasks", Args: map[string]any{"project": "Work"}}},
		}}}}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &GeminiProvider{APIKey: "test-key", BaseURL: srv.URL}
	tools := []ToolSpec{{Name: "list_tasks", Description: "list tasks", Parameters: map[string]any{"type": "object"}}}
	got, err := p.Chat(context.Background(), "system", []Turn{{Role: "user", Content: "what's open in Work?"}}, tools)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "" || len(got.ToolCalls) != 1 {
		t.Fatalf("Chat() = %+v", got)
	}
	call := got.ToolCalls[0]
	if call.ID == "" || call.Name != "list_tasks" || call.Arguments["project"] != "Work" {
		t.Fatalf("ToolCalls[0] = %+v", call)
	}
}

// TestGeminiChatRoundTripsToolResult checks that a "tool" Turn is correlated
// back to the right function by name, recovered from the synthesized call
// ID (geminiCallNameFromID) — Gemini has no call-id concept of its own.
func TestGeminiChatRoundTripsToolResult(t *testing.T) {
	var gotReq geminiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatal(err)
		}
		var resp geminiResponse
		resp.Candidates = []struct {
			Content geminiContent `json:"content"`
		}{{Content: geminiContent{Role: "model", Parts: []geminiPart{{Text: "You have one open task."}}}}}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	history := []Turn{
		{Role: "user", Content: "what's open?"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "list_tasks#0", Name: "list_tasks", Arguments: map[string]any{}}}},
		{Role: "tool", ToolCallID: "list_tasks#0", Content: `[{"id":"1","title":"Ship report"}]`},
	}
	p := &GeminiProvider{APIKey: "test-key", BaseURL: srv.URL}
	got, err := p.Chat(context.Background(), "system", history, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "You have one open task." {
		t.Fatalf("Chat() = %+v", got)
	}
	if len(gotReq.Contents) != 3 {
		t.Fatalf("len(Contents) = %d, want 3: %+v", len(gotReq.Contents), gotReq.Contents)
	}
	fr := gotReq.Contents[2].Parts[0].FunctionResponse
	if fr == nil || fr.Name != "list_tasks" {
		t.Fatalf("FunctionResponse = %+v", fr)
	}
}
