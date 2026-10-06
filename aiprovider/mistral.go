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

const mistralDefaultModel = "mistral-large-latest"

// MistralProvider is a TextProvider backed by Mistral's OpenAI-compatible
// chat completions endpoint (https://docs.mistral.ai/api/). It does not
// implement TranscriptionProvider: this package only wires up Mistral's
// text/tool-calling API, not its separate Voxtral audio endpoint, so a
// Mistral-configured text provider still needs an OpenAI key for recording
// (NewTranscriber).
type MistralProvider struct {
	APIKey  string
	Model   string // defaults to mistralDefaultModel
	BaseURL string // overridden by tests
	Client  *http.Client
}

func NewMistral(apiKey string) *MistralProvider { return &MistralProvider{APIKey: apiKey} }

func (p *MistralProvider) Name() string { return "Mistral" }

func (p *MistralProvider) model() string {
	if p.Model != "" {
		return p.Model
	}
	return mistralDefaultModel
}

func (p *MistralProvider) baseURL() string {
	if p.BaseURL != "" {
		return p.BaseURL
	}
	return "https://api.mistral.ai"
}

func (p *MistralProvider) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return &http.Client{Timeout: 180 * time.Second}
}

// mistralMessage and the tool-calling types below mirror openai.go's own —
// Mistral's chat completions API is explicitly OpenAI-compatible for this
// shape — kept as this file's own types rather than shared ones so each
// provider file stays independently editable if the two APIs ever drift,
// the same way Anthropic's and Gemini's files duplicate rather than share
// overlapping concepts with OpenAI's.
type mistralMessage struct {
	Role       string            `json:"role"`
	Content    string            `json:"content,omitempty"`
	ToolCalls  []mistralToolCall `json:"tool_calls,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
}

type mistralToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // always "function"
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON-encoded object
	} `json:"function"`
}

type mistralTool struct {
	Type     string `json:"type"` // always "function"
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type mistralChatRequest struct {
	Model    string           `json:"model"`
	Messages []mistralMessage `json:"messages"`
	Tools    []mistralTool    `json:"tools,omitempty"`
}

type mistralChoice struct {
	Message mistralMessage `json:"message"`
}

type mistralChatResponse struct {
	Choices []mistralChoice `json:"choices"`
	// Mistral's error responses use "message" directly at the top level
	// rather than OpenAI's nested {"error":{"message":...}} — this field
	// catches that shape; the nested one below catches requests that do
	// come back OpenAI-shaped (the two APIs are not identical here).
	Message string `json:"message"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (p *MistralProvider) call(ctx context.Context, prompt string) (string, error) {
	if p.APIKey == "" {
		return "", ErrNoAPIKey
	}
	reqBody, err := json.Marshal(mistralChatRequest{
		Model:    p.model(),
		Messages: []mistralMessage{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL()+"/v1/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.APIKey)

	resp, err := p.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("mistral request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("mistral response body: %w", err)
	}
	var out mistralChatResponse
	if jsonErr := json.Unmarshal(data, &out); jsonErr != nil {
		return "", fmt.Errorf("mistral response (status %d): %w", resp.StatusCode, jsonErr)
	}
	if resp.StatusCode != http.StatusOK {
		msg := out.Message
		if out.Error != nil && out.Error.Message != "" {
			msg = out.Error.Message
		}
		if msg == "" {
			msg = fmt.Sprintf("unexpected status %d", resp.StatusCode)
		}
		return "", fmt.Errorf("mistral: %s", msg)
	}
	if len(out.Choices) == 0 || out.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("mistral: empty response")
	}
	return out.Choices[0].Message.Content, nil
}

func (p *MistralProvider) Summarize(ctx context.Context, transcript string) (string, error) {
	if err := checkTranscriptLen(transcript); err != nil {
		return "", err
	}
	text, err := p.call(ctx, summarizePrompt(transcript))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(text), nil
}

func (p *MistralProvider) ExtractActionItems(ctx context.Context, transcript, summary string, existingProjects, existingTags []string) ([]Suggestion, error) {
	if err := checkTranscriptLen(transcript); err != nil {
		return nil, err
	}
	text, err := p.call(ctx, extractActionItemsPrompt(transcript, summary, existingProjects, existingTags))
	if err != nil {
		return nil, err
	}
	return parseSuggestionsJSON(text)
}

// ── Chat (tool-calling) ──────────────────────────────────────────────────────

func mistralTurnsToMessages(system string, history []Turn) []mistralMessage {
	msgs := make([]mistralMessage, 0, len(history)+1)
	msgs = append(msgs, mistralMessage{Role: "system", Content: system})
	for _, t := range history {
		switch t.Role {
		case "tool":
			msgs = append(msgs, mistralMessage{Role: "tool", Content: t.Content, ToolCallID: t.ToolCallID})
		case "assistant":
			m := mistralMessage{Role: "assistant", Content: t.Content}
			for _, c := range t.ToolCalls {
				argsJSON, _ := json.Marshal(c.Arguments)
				tc := mistralToolCall{ID: c.ID, Type: "function"}
				tc.Function.Name = c.Name
				tc.Function.Arguments = string(argsJSON)
				m.ToolCalls = append(m.ToolCalls, tc)
			}
			msgs = append(msgs, m)
		default:
			msgs = append(msgs, mistralMessage{Role: "user", Content: t.Content})
		}
	}
	return msgs
}

func mistralToolSpecsToTools(tools []ToolSpec) []mistralTool {
	out := make([]mistralTool, len(tools))
	for i, t := range tools {
		out[i].Type = "function"
		out[i].Function.Name = t.Name
		out[i].Function.Description = t.Description
		out[i].Function.Parameters = t.Parameters
	}
	return out
}

// Chat is call reworked for multiple turns and tools — see openai.go's Chat,
// which this mirrors almost exactly given the two APIs' shared shape here.
func (p *MistralProvider) Chat(ctx context.Context, system string, history []Turn, tools []ToolSpec) (ChatResult, error) {
	if p.APIKey == "" {
		return ChatResult{}, ErrNoAPIKey
	}
	reqBody, err := json.Marshal(mistralChatRequest{
		Model:    p.model(),
		Messages: mistralTurnsToMessages(system, history),
		Tools:    mistralToolSpecsToTools(tools),
	})
	if err != nil {
		return ChatResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL()+"/v1/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return ChatResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.APIKey)

	resp, err := p.client().Do(req)
	if err != nil {
		return ChatResult{}, fmt.Errorf("mistral request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return ChatResult{}, fmt.Errorf("mistral response body: %w", err)
	}
	var out mistralChatResponse
	if jsonErr := json.Unmarshal(data, &out); jsonErr != nil {
		return ChatResult{}, fmt.Errorf("mistral response (status %d): %w", resp.StatusCode, jsonErr)
	}
	if resp.StatusCode != http.StatusOK {
		msg := out.Message
		if out.Error != nil && out.Error.Message != "" {
			msg = out.Error.Message
		}
		if msg == "" {
			msg = fmt.Sprintf("unexpected status %d", resp.StatusCode)
		}
		return ChatResult{}, fmt.Errorf("mistral: %s", msg)
	}
	if len(out.Choices) == 0 {
		return ChatResult{}, fmt.Errorf("mistral: empty response")
	}
	msg := out.Choices[0].Message
	if len(msg.ToolCalls) > 0 {
		calls := make([]ToolCall, len(msg.ToolCalls))
		for i, tc := range msg.ToolCalls {
			var args map[string]any
			if tc.Function.Arguments != "" {
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
					return ChatResult{}, fmt.Errorf("mistral: decoding tool call arguments: %w", err)
				}
			}
			calls[i] = ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: args}
		}
		return ChatResult{ToolCalls: calls}, nil
	}
	return ChatResult{Text: strings.TrimSpace(msg.Content)}, nil
}
