package aiprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// geminiDefaultModel matches the model the AI-Kompetenceportal compliance
// portal already uses (AI-comliance/server.ts) — keeping the same default
// across both of this account's projects rather than picking a different one
// here for no reason.
const geminiDefaultModel = "gemini-3.8-flash"

// GeminiProvider is a TextProvider backed by the generateContent REST
// endpoint. It does not implement TranscriptionProvider: Gemini's public API
// has no dedicated speech-to-text endpoint comparable to Whisper's.
type GeminiProvider struct {
	APIKey  string
	Model   string // defaults to geminiDefaultModel
	BaseURL string // overridden by tests
	Client  *http.Client
}

func NewGemini(apiKey string) *GeminiProvider { return &GeminiProvider{APIKey: apiKey} }

func (p *GeminiProvider) Name() string { return "Gemini" }

func (p *GeminiProvider) model() string {
	if p.Model != "" {
		return p.Model
	}
	return geminiDefaultModel
}

func (p *GeminiProvider) baseURL() string {
	if p.BaseURL != "" {
		return p.BaseURL
	}
	return "https://generativelanguage.googleapis.com"
}

func (p *GeminiProvider) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return &http.Client{Timeout: 120 * time.Second}
}

// geminiPart covers every part shape this file sends or receives: plain
// text, a model-issued functionCall, or this file's reply to one
// (functionResponse) — Gemini has no separate envelope for each the way
// OpenAI's tool_calls/content split does, it is all just "parts" on a
// content with the right role.
type geminiPart struct {
	Text             string                `json:"text,omitempty"`
	FunctionCall     *geminiFunctionCall   `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResult `json:"functionResponse,omitempty"`
}
type geminiFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}
type geminiFunctionResult struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}
type geminiContent struct {
	Role  string       `json:"role,omitempty"` // "user" | "model"; omitted for system_instruction
	Parts []geminiPart `json:"parts"`
}
type geminiTool struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"function_declarations"`
}
type geminiFunctionDeclaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}
type geminiRequest struct {
	Contents          []geminiContent `json:"contents"`
	SystemInstruction *geminiContent  `json:"system_instruction,omitempty"`
	Tools             []geminiTool    `json:"tools,omitempty"`
}
type geminiResponse struct {
	Candidates []struct {
		Content geminiContent `json:"content"`
	} `json:"candidates"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (p *GeminiProvider) call(ctx context.Context, prompt string) (string, error) {
	if p.APIKey == "" {
		return "", ErrNoAPIKey
	}
	reqBody, err := json.Marshal(geminiRequest{
		Contents: []geminiContent{{Parts: []geminiPart{{Text: prompt}}}},
	})
	if err != nil {
		return "", err
	}
	endpoint := p.baseURL() + "/v1beta/models/" + p.model() + ":generateContent?key=" + url.QueryEscape(p.APIKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("gemini request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("gemini response body: %w", err)
	}
	var out geminiResponse
	if jsonErr := json.Unmarshal(data, &out); jsonErr != nil {
		return "", fmt.Errorf("gemini response (status %d): %w", resp.StatusCode, jsonErr)
	}
	if out.Error != nil {
		return "", fmt.Errorf("gemini: %s", out.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gemini: unexpected status %d", resp.StatusCode)
	}
	if len(out.Candidates) == 0 || len(out.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("gemini: empty response")
	}
	var text strings.Builder
	for _, part := range out.Candidates[0].Content.Parts {
		text.WriteString(part.Text)
	}
	if text.Len() == 0 {
		return "", fmt.Errorf("gemini: empty response")
	}
	return text.String(), nil
}

func (p *GeminiProvider) Summarize(ctx context.Context, transcript string) (string, error) {
	if err := checkTranscriptLen(transcript); err != nil {
		return "", err
	}
	text, err := p.call(ctx, summarizePrompt(transcript))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(text), nil
}

func (p *GeminiProvider) ExtractActionItems(ctx context.Context, transcript, summary string, existingProjects, existingTags []string) ([]Suggestion, error) {
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

// geminiSynthesizeCallID and geminiCallNameFromID exist because Gemini,
// unlike OpenAI/Anthropic, assigns no id to a functionCall part — a
// functionResponse is correlated back to it by function name alone. ToolCall
// still needs a non-empty, unique ID (every other provider has one, and
// chatops.go treats ID as opaque), so one is synthesized as "name#index"
// within the response's part list, and recovered from that same string when
// a "tool" Turn is encoded back into a functionResponse part.
func geminiSynthesizeCallID(name string, index int) string {
	return fmt.Sprintf("%s#%d", name, index)
}

func geminiCallNameFromID(id string) string {
	if i := strings.LastIndexByte(id, '#'); i >= 0 {
		return id[:i]
	}
	return id
}

func geminiTurnsToContents(history []Turn) []geminiContent {
	out := make([]geminiContent, 0, len(history))
	for _, t := range history {
		switch t.Role {
		case "assistant":
			if len(t.ToolCalls) == 0 {
				out = append(out, geminiContent{Role: "model", Parts: []geminiPart{{Text: t.Content}}})
				continue
			}
			parts := make([]geminiPart, 0, len(t.ToolCalls))
			for _, c := range t.ToolCalls {
				parts = append(parts, geminiPart{FunctionCall: &geminiFunctionCall{Name: c.Name, Args: c.Arguments}})
			}
			out = append(out, geminiContent{Role: "model", Parts: parts})
		case "tool":
			out = append(out, geminiContent{Role: "user", Parts: []geminiPart{{
				FunctionResponse: &geminiFunctionResult{
					Name:     geminiCallNameFromID(t.ToolCallID),
					Response: map[string]any{"result": t.Content},
				},
			}}})
		default:
			out = append(out, geminiContent{Role: "user", Parts: []geminiPart{{Text: t.Content}}})
		}
	}
	return out
}

func geminiToolSpecsToTools(tools []ToolSpec) []geminiTool {
	if len(tools) == 0 {
		return nil
	}
	decls := make([]geminiFunctionDeclaration, len(tools))
	for i, t := range tools {
		decls[i] = geminiFunctionDeclaration(t)
	}
	return []geminiTool{{FunctionDeclarations: decls}}
}

// Chat is call reworked for multiple turns and tools: system moves to its
// own system_instruction field, history replaces the single prompt content,
// and a response may come back as one or more functionCall parts instead of
// text.
func (p *GeminiProvider) Chat(ctx context.Context, system string, history []Turn, tools []ToolSpec) (ChatResult, error) {
	if p.APIKey == "" {
		return ChatResult{}, ErrNoAPIKey
	}
	req := geminiRequest{
		Contents: geminiTurnsToContents(history),
		Tools:    geminiToolSpecsToTools(tools),
	}
	if system != "" {
		req.SystemInstruction = &geminiContent{Parts: []geminiPart{{Text: system}}}
	}
	reqBody, err := json.Marshal(req)
	if err != nil {
		return ChatResult{}, err
	}
	endpoint := p.baseURL() + "/v1beta/models/" + p.model() + ":generateContent?key=" + url.QueryEscape(p.APIKey)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return ChatResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client().Do(httpReq)
	if err != nil {
		return ChatResult{}, fmt.Errorf("gemini request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return ChatResult{}, fmt.Errorf("gemini response body: %w", err)
	}
	var out geminiResponse
	if jsonErr := json.Unmarshal(data, &out); jsonErr != nil {
		return ChatResult{}, fmt.Errorf("gemini response (status %d): %w", resp.StatusCode, jsonErr)
	}
	if out.Error != nil {
		return ChatResult{}, fmt.Errorf("gemini: %s", out.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return ChatResult{}, fmt.Errorf("gemini: unexpected status %d", resp.StatusCode)
	}
	if len(out.Candidates) == 0 {
		return ChatResult{}, fmt.Errorf("gemini: empty response")
	}
	var calls []ToolCall
	var text strings.Builder
	for i, part := range out.Candidates[0].Content.Parts {
		switch {
		case part.FunctionCall != nil:
			calls = append(calls, ToolCall{
				ID:        geminiSynthesizeCallID(part.FunctionCall.Name, i),
				Name:      part.FunctionCall.Name,
				Arguments: part.FunctionCall.Args,
			})
		case part.Text != "":
			text.WriteString(part.Text)
		}
	}
	if len(calls) > 0 {
		return ChatResult{ToolCalls: calls}, nil
	}
	if text.Len() == 0 {
		return ChatResult{}, fmt.Errorf("gemini: empty response")
	}
	return ChatResult{Text: text.String()}, nil
}
