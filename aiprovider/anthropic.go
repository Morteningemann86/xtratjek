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

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
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
		Messages:  []anthropicMessage{{Role: "user", Content: prompt}},
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
