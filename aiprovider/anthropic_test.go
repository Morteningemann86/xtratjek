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

func TestAnthropicChatText(t *testing.T) {
	var gotReq anthropicRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatal(err)
		}
		json.NewEncoder(w).Encode(anthropicResponse{
			Content: []anthropicContentBlock{{Type: "text", Text: "hi there"}},
		})
	}))
	defer srv.Close()

	p := &AnthropicProvider{APIKey: "test-key", BaseURL: srv.URL}
	got, err := p.Chat(context.Background(), "system prompt", []Turn{{Role: "user", Content: "hello"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "hi there" || len(got.ToolCalls) != 0 {
		t.Fatalf("Chat() = %+v", got)
	}
	if gotReq.System != "system prompt" {
		t.Fatalf("System = %q", gotReq.System)
	}
	if len(gotReq.Messages) != 1 || gotReq.Messages[0].Role != "user" {
		t.Fatalf("Messages = %+v", gotReq.Messages)
	}
}

func TestAnthropicChatToolUse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(anthropicResponse{
			Content: []anthropicContentBlock{{Type: "tool_use", ID: "toolu_1", Name: "list_tasks", Input: map[string]any{"project": "Work"}}},
		})
	}))
	defer srv.Close()

	p := &AnthropicProvider{APIKey: "test-key", BaseURL: srv.URL}
	tools := []ToolSpec{{Name: "list_tasks", Description: "list tasks", Parameters: map[string]any{"type": "object"}}}
	got, err := p.Chat(context.Background(), "system", []Turn{{Role: "user", Content: "what's open in Work?"}}, tools)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "" || len(got.ToolCalls) != 1 {
		t.Fatalf("Chat() = %+v", got)
	}
	call := got.ToolCalls[0]
	if call.ID != "toolu_1" || call.Name != "list_tasks" || call.Arguments["project"] != "Work" {
		t.Fatalf("ToolCalls[0] = %+v", call)
	}
}

// TestAnthropicChatRoundTripsToolResult checks the shape anthropicTurnsToMessages
// builds for a resumed hop: the assistant's prior tool_use is echoed back as
// an assistant message with a tool_use block, and the tool's result rides in
// a *user* message as a tool_result block — Anthropic has no "tool" role of
// its own, unlike OpenAI.
func TestAnthropicChatRoundTripsToolResult(t *testing.T) {
	var gotReq anthropicRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatal(err)
		}
		json.NewEncoder(w).Encode(anthropicResponse{
			Content: []anthropicContentBlock{{Type: "text", Text: "You have one open task."}},
		})
	}))
	defer srv.Close()

	history := []Turn{
		{Role: "user", Content: "what's open?"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "toolu_1", Name: "list_tasks", Arguments: map[string]any{}}}},
		{Role: "tool", ToolCallID: "toolu_1", Content: `[{"id":"1","title":"Ship report"}]`},
	}
	p := &AnthropicProvider{APIKey: "test-key", BaseURL: srv.URL}
	got, err := p.Chat(context.Background(), "system", history, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "You have one open task." {
		t.Fatalf("Chat() = %+v", got)
	}
	if len(gotReq.Messages) != 3 {
		t.Fatalf("len(Messages) = %d, want 3: %+v", len(gotReq.Messages), gotReq.Messages)
	}
	var assistantBlocks []anthropicContentBlock
	if err := json.Unmarshal(gotReq.Messages[1].Content, &assistantBlocks); err != nil {
		t.Fatal(err)
	}
	if len(assistantBlocks) != 1 || assistantBlocks[0].Type != "tool_use" || assistantBlocks[0].ID != "toolu_1" {
		t.Fatalf("assistant message blocks = %+v", assistantBlocks)
	}
	if gotReq.Messages[2].Role != "user" {
		t.Fatalf("tool result message role = %q, want user", gotReq.Messages[2].Role)
	}
	var resultBlocks []anthropicContentBlock
	if err := json.Unmarshal(gotReq.Messages[2].Content, &resultBlocks); err != nil {
		t.Fatal(err)
	}
	if len(resultBlocks) != 1 || resultBlocks[0].Type != "tool_result" || resultBlocks[0].ToolUseID != "toolu_1" {
		t.Fatalf("tool result message blocks = %+v", resultBlocks)
	}
}
