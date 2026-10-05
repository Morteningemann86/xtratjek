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

type geminiPart struct {
	Text string `json:"text"`
}
type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}
type geminiRequest struct {
	Contents []geminiContent `json:"contents"`
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
