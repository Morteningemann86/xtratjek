package aiprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// anthropicDefaultModel is used when AnthropicProvider.Model is empty.
// Overridable per-instance for anyone who wants to pin a specific dated
// snapshot instead of the rolling alias.
const anthropicDefaultModel = "claude-sonnet-4-5"

// AnthropicProvider is a TextProvider backed by the Messages API
// (https://docs.anthropic.com/en/api/messages). It does not implement
// TranscriptionProvider: Anthropic has no speech-to-text endpoint.
type AnthropicProvider struct {
	APIKey string
	Model  string // defaults to anthropicDefaultModel
	// BaseURL overrides the real API host; set by tests against an httptest
	// server, left empty in production.
	BaseURL string
	Client  *http.Client
}

func NewAnthropic(apiKey string) *AnthropicProvider { return &AnthropicProvider{APIKey: apiKey} }

func (p *AnthropicProvider) Name() string { return "Anthropic" }

func (p *AnthropicProvider) model() string {
	if p.Model != "" {
		return p.Model
	}
	return anthropicDefaultModel
}

func (p *AnthropicProvider) baseURL() string {
	if p.BaseURL != "" {
		return p.BaseURL
	}
	return "https://api.anthropic.com"
}

func (p *AnthropicProvider) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return &http.Client{Timeout: 120 * time.Second}
}

// anthropicMessage's Content is a plain string for Summarize/
// ExtractActionItems' one-shot prompts (call below) but a slice of content
// blocks once Chat is in play — tool_use and tool_result both need
// structure a bare string can't carry — so it is typed as json.RawMessage
// and each caller decides which shape to marshal.
type anthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// anthropicContentBlock covers every block shape this file sends or
// receives: Type "text" (Text set), "tool_use" (ID/Name/Input set, in a
// response or an echoed-back assistant message) and "tool_result"
// (ToolUseID/Content set, sent back as part of a user message).
type anthropicContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     map[string]any  `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
}

type anthropicResponse struct {
	Content []anthropicContentBlock `json:"content"`
	Error   *anthropicAPIError      `json:"error"`
}

type anthropicAPIError struct {
	Message string `json:"message"`
}

// call sends one single-turn prompt and returns the concatenated text blocks
// of the reply. Both Summarize and ExtractActionItems are one-shot requests —
// there is no multi-turn conversation in this feature — so this is the only
// request shape AnthropicProvider needs.
func (p *AnthropicProvider) call(ctx context.Context, prompt string, maxTokens int) (string, error) {
	if p.APIKey == "" {
		return "", ErrNoAPIKey
	}
	reqBody, err := json.Marshal(anthropicRequest{
		Model:     p.model(),
		MaxTokens: maxTokens,
		Messages:  []anthropicMessage{{Role: "user", Content: anthropicTextContent(prompt)}},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL()+"/v1/messages", bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("anthropic request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("anthropic response body: %w", err)
	}
	var out anthropicResponse
	if jsonErr := json.Unmarshal(data, &out); jsonErr != nil {
		return "", fmt.Errorf("anthropic response (status %d): %w", resp.StatusCode, jsonErr)
	}
	if out.Error != nil {
		return "", fmt.Errorf("anthropic: %s", out.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("anthropic: unexpected status %d", resp.StatusCode)
	}
	var text strings.Builder
	for _, b := range out.Content {
		if b.Type == "text" {
			text.WriteString(b.Text)
		}
	}
	if text.Len() == 0 {
		return "", fmt.Errorf("anthropic: empty response")
	}
	return text.String(), nil
}

func (p *AnthropicProvider) Summarize(ctx context.Context, transcript string) (string, error) {
	if err := checkTranscriptLen(transcript); err != nil {
		return "", err
	}
	text, err := p.call(ctx, summarizePrompt(transcript), 1024)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(text), nil
}

func (p *AnthropicProvider) ExtractActionItems(ctx context.Context, transcript, summary string, existingProjects, existingTags []string) ([]Suggestion, error) {
	if err := checkTranscriptLen(transcript); err != nil {
		return nil, err
	}
	text, err := p.call(ctx, extractActionItemsPrompt(transcript, summary, existingProjects, existingTags), 4096)
	if err != nil {
		return nil, err
	}
	return parseSuggestionsJSON(text)
}

// ── Chat (tool-calling) ──────────────────────────────────────────────────────

const anthropicChatMaxTokens = 2048

func anthropicTextContent(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

func anthropicBlocksContent(blocks []anthropicContentBlock) json.RawMessage {
	b, _ := json.Marshal(blocks)
	return b
}

// anthropicTurnsToMessages lays history out as Anthropic's Messages API
// expects. Its protocol has no "tool" role of its own: a Turn answering a
// tool call rides in a user message as a tool_result block, and an
// assistant Turn that asked for tools is echoed back as a tool_use block
// rather than plain text — both require Content to carry blocks, which is
// why anthropicMessage.Content is json.RawMessage rather than a plain
// string (see its doc comment).
func anthropicTurnsToMessages(history []Turn) []anthropicMessage {
	msgs := make([]anthropicMessage, 0, len(history))
	for _, t := range history {
		switch t.Role {
		case "assistant":
			if len(t.ToolCalls) == 0 {
				msgs = append(msgs, anthropicMessage{Role: "assistant", Content: anthropicTextContent(t.Content)})
				continue
			}
			blocks := make([]anthropicContentBlock, 0, len(t.ToolCalls))
			for _, c := range t.ToolCalls {
				blocks = append(blocks, anthropicContentBlock{Type: "tool_use", ID: c.ID, Name: c.Name, Input: c.Arguments})
			}
			msgs = append(msgs, anthropicMessage{Role: "assistant", Content: anthropicBlocksContent(blocks)})
		case "tool":
			block := anthropicContentBlock{Type: "tool_result", ToolUseID: t.ToolCallID, Content: anthropicTextContent(t.Content)}
			msgs = append(msgs, anthropicMessage{Role: "user", Content: anthropicBlocksContent([]anthropicContentBlock{block})})
		default:
			msgs = append(msgs, anthropicMessage{Role: "user", Content: anthropicTextContent(t.Content)})
		}
	}
	return msgs
}

func anthropicToolSpecsToTools(tools []ToolSpec) []anthropicTool {
	out := make([]anthropicTool, len(tools))
	for i, t := range tools {
		out[i] = anthropicTool{Name: t.Name, Description: t.Description, InputSchema: t.Parameters}
	}
	return out
}

// Chat is call reworked for multiple turns and tools: same request/response
// plumbing and error handling, but system moves to its own top-level field
// (Anthropic's convention, unlike OpenAI's system-role message) and a
// response may come back as one or more tool_use blocks instead of text.
func (p *AnthropicProvider) Chat(ctx context.Context, system string, history []Turn, tools []ToolSpec) (ChatResult, error) {
	if p.APIKey == "" {
		return ChatResult{}, ErrNoAPIKey
	}
	reqBody, err := json.Marshal(anthropicRequest{
		Model:     p.model(),
		MaxTokens: anthropicChatMaxTokens,
		System:    system,
		Messages:  anthropicTurnsToMessages(history),
		Tools:     anthropicToolSpecsToTools(tools),
	})
	if err != nil {
		return ChatResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL()+"/v1/messages", bytes.NewReader(reqBody))
	if err != nil {
		return ChatResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.client().Do(req)
	if err != nil {
		return ChatResult{}, fmt.Errorf("anthropic request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return ChatResult{}, fmt.Errorf("anthropic response body: %w", err)
	}
	var out anthropicResponse
	if jsonErr := json.Unmarshal(data, &out); jsonErr != nil {
		return ChatResult{}, fmt.Errorf("anthropic response (status %d): %w", resp.StatusCode, jsonErr)
	}
	if out.Error != nil {
		return ChatResult{}, fmt.Errorf("anthropic: %s", out.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return ChatResult{}, fmt.Errorf("anthropic: unexpected status %d", resp.StatusCode)
	}
	var calls []ToolCall
	var text strings.Builder
	for _, b := range out.Content {
		switch b.Type {
		case "text":
			text.WriteString(b.Text)
		case "tool_use":
			calls = append(calls, ToolCall{ID: b.ID, Name: b.Name, Arguments: b.Input})
		}
	}
	if len(calls) > 0 {
		return ChatResult{ToolCalls: calls}, nil
	}
	if text.Len() == 0 {
		return ChatResult{}, fmt.Errorf("anthropic: empty response")
	}
	return ChatResult{Text: text.String()}, nil
}
