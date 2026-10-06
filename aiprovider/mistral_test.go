package aiprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMistralSummarize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization header = %q", got)
		}
		resp := mistralChatResponse{Choices: []mistralChoice{{}}}
		resp.Choices[0].Message.Content = "  The summary.  "
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &MistralProvider{APIKey: "test-key", BaseURL: srv.URL}
	got, err := p.Summarize(context.Background(), "notes")
	if err != nil {
		t.Fatal(err)
	}
	if got != "The summary." {
		t.Fatalf("Summarize() = %q", got)
	}
}

func TestMistralExtractActionItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := mistralChatResponse{Choices: []mistralChoice{{}}}
		resp.Choices[0].Message.Content = `[{"title":"Follow up with legal","project":"","tags":[],"priority":"m","due":""}]`
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &MistralProvider{APIKey: "test-key", BaseURL: srv.URL}
	got, err := p.ExtractActionItems(context.Background(), "t", "s", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "Follow up with legal" {
		t.Fatalf("ExtractActionItems() = %+v", got)
	}
}

func TestMistralNoAPIKey(t *testing.T) {
	p := &MistralProvider{}
	if _, err := p.Summarize(context.Background(), "x"); err != ErrNoAPIKey {
		t.Fatalf("err = %v, want ErrNoAPIKey", err)
	}
	if _, err := p.Chat(context.Background(), "sys", nil, nil); err != ErrNoAPIKey {
		t.Fatalf("Chat err = %v, want ErrNoAPIKey", err)
	}
}

func TestMistralAPIErrorTopLevelMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(mistralChatResponse{Message: "invalid api key"})
	}))
	defer srv.Close()

	p := &MistralProvider{APIKey: "bad-key", BaseURL: srv.URL}
	_, err := p.Summarize(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "invalid api key") {
		t.Fatalf("err = %v, want it to mention invalid api key", err)
	}
}

func TestMistralChatText(t *testing.T) {
	var gotReq mistralChatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatal(err)
		}
		resp := mistralChatResponse{Choices: []mistralChoice{{}}}
		resp.Choices[0].Message.Content = "  hi there  "
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &MistralProvider{APIKey: "test-key", BaseURL: srv.URL}
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
}

func TestMistralChatToolCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := mistralChatResponse{Choices: []mistralChoice{{}}}
		tc := mistralToolCall{ID: "call_1", Type: "function"}
		tc.Function.Name = "list_tasks"
		tc.Function.Arguments = `{"project":"Work"}`
		resp.Choices[0].Message.ToolCalls = []mistralToolCall{tc}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &MistralProvider{APIKey: "test-key", BaseURL: srv.URL}
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

func TestMistralChatRoundTripsToolResult(t *testing.T) {
	var gotReq mistralChatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatal(err)
		}
		resp := mistralChatResponse{Choices: []mistralChoice{{}}}
		resp.Choices[0].Message.Content = "You have one open task."
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	history := []Turn{
		{Role: "user", Content: "what's open?"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "list_tasks", Arguments: map[string]any{}}}},
		{Role: "tool", ToolCallID: "call_1", Content: `[{"id":"1","title":"Ship report"}]`},
	}
	p := &MistralProvider{APIKey: "test-key", BaseURL: srv.URL}
	got, err := p.Chat(context.Background(), "system", history, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "You have one open task." {
		t.Fatalf("Chat() = %+v", got)
	}
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
