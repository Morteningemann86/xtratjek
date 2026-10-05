package aiprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	openAIDefaultChatModel    = "gpt-5-mini"
	openAIDefaultWhisperModel = "whisper-1"
)

// OpenAIProvider implements both TextProvider and TranscriptionProvider — the
// only provider here that does both, since it is currently the only one of
// the three with a speech-to-text endpoint. A user whose main text provider
// is Anthropic or Gemini still needs an OpenAI key configured to record and
// transcribe meetings; see NewTranscriber.
type OpenAIProvider struct {
	APIKey       string
	ChatModel    string // defaults to openAIDefaultChatModel
	WhisperModel string // defaults to openAIDefaultWhisperModel
	BaseURL      string // overridden by tests
	Client       *http.Client
}

func NewOpenAI(apiKey string) *OpenAIProvider { return &OpenAIProvider{APIKey: apiKey} }

func (p *OpenAIProvider) Name() string { return "OpenAI" }

func (p *OpenAIProvider) chatModel() string {
	if p.ChatModel != "" {
		return p.ChatModel
	}
	return openAIDefaultChatModel
}

func (p *OpenAIProvider) whisperModel() string {
	if p.WhisperModel != "" {
		return p.WhisperModel
	}
	return openAIDefaultWhisperModel
}

func (p *OpenAIProvider) baseURL() string {
	if p.BaseURL != "" {
		return p.BaseURL
	}
	return "https://api.openai.com"
}

func (p *OpenAIProvider) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return &http.Client{Timeout: 180 * time.Second}
}

type openAIChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatRequest struct {
	Model    string              `json:"model"`
	Messages []openAIChatMessage `json:"messages"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (p *OpenAIProvider) call(ctx context.Context, prompt string) (string, error) {
	if p.APIKey == "" {
		return "", ErrNoAPIKey
	}
	reqBody, err := json.Marshal(openAIChatRequest{
		Model:    p.chatModel(),
		Messages: []openAIChatMessage{{Role: "user", Content: prompt}},
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
		return "", fmt.Errorf("openai request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("openai response body: %w", err)
	}
	var out openAIChatResponse
	if jsonErr := json.Unmarshal(data, &out); jsonErr != nil {
		return "", fmt.Errorf("openai response (status %d): %w", resp.StatusCode, jsonErr)
	}
	if out.Error != nil {
		return "", fmt.Errorf("openai: %s", out.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openai: unexpected status %d", resp.StatusCode)
	}
	if len(out.Choices) == 0 || out.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("openai: empty response")
	}
	return out.Choices[0].Message.Content, nil
}

func (p *OpenAIProvider) Summarize(ctx context.Context, transcript string) (string, error) {
	if err := checkTranscriptLen(transcript); err != nil {
		return "", err
	}
	text, err := p.call(ctx, summarizePrompt(transcript))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(text), nil
}

func (p *OpenAIProvider) ExtractActionItems(ctx context.Context, transcript, summary string, existingProjects, existingTags []string) ([]Suggestion, error) {
	if err := checkTranscriptLen(transcript); err != nil {
		return nil, err
	}
	text, err := p.call(ctx, extractActionItemsPrompt(transcript, summary, existingProjects, existingTags))
	if err != nil {
		return nil, err
	}
	return parseSuggestionsJSON(text)
}

// ── Transcription (Whisper) ────────────────────────────────────────────────

type openAITranscriptionResponse struct {
	Text  string `json:"text"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Transcribe uploads the wav file at audioPath to the Whisper endpoint and
// returns its text. Whisper caps a single request at 25MB — well above a
// typical meeting at the 16kHz mono rate audiorecorder.go records at (roughly
// 115MB/hour, so about 3 hours fits) — a bigger file fails with OpenAI's own
// "file too large" error, which the caller surfaces as-is rather than this
// package trying to chunk or re-encode audio.
func (p *OpenAIProvider) Transcribe(ctx context.Context, audioPath string) (string, error) {
	if p.APIKey == "" {
		return "", ErrNoAPIKey
	}
	f, err := os.Open(audioPath)
	if err != nil {
		return "", fmt.Errorf("opening recording: %w", err)
	}
	defer f.Close()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filepath.Base(audioPath))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(fw, f); err != nil {
		return "", fmt.Errorf("reading recording: %w", err)
	}
	if err := mw.WriteField("model", p.whisperModel()); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL()+"/v1/audio/transcriptions", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+p.APIKey)

	resp, err := p.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("openai transcription request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("openai transcription response body: %w", err)
	}
	var out openAITranscriptionResponse
	if jsonErr := json.Unmarshal(data, &out); jsonErr != nil {
		return "", fmt.Errorf("openai transcription response (status %d): %w", resp.StatusCode, jsonErr)
	}
	if out.Error != nil {
		return "", fmt.Errorf("openai transcription: %s", out.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openai transcription: unexpected status %d", resp.StatusCode)
	}
	return strings.TrimSpace(out.Text), nil
}
