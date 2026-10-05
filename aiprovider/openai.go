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
	Content string `json:"content,omitempty"`
	// ToolCalls is set on an assistant message that called tools instead of
	// answering in Content. ToolCallID is set the other direction, on the
	// "tool" message answering one of them — see chatTurnsToOpenAI.
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // always "function"
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON-encoded object, per OpenAI's wire format
	} `json:"function"`
}

type openAITool struct {
	Type     string `json:"type"` // always "function"
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type openAIChatRequest struct {
	Model    string              `json:"model"`
	Messages []openAIChatMessage `json:"messages"`
	Tools    []openAITool        `json:"tools,omitempty"`
}

type openAIChoice struct {
	Message openAIChatMessage `json:"message"`
}

type openAIChatResponse struct {
	Choices []openAIChoice `json:"choices"`
	Error   *struct {
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

// ── Chat (tool-calling) ──────────────────────────────────────────────────────

// openAITurnsToMessages lays system and history out as OpenAI's own message
// list, which the Chat tab's Turn is already close to: a "tool" Turn is
// OpenAI's own "tool" role, and an assistant Turn's ToolCalls round-trips
// through openAIToolCall with its Arguments re-encoded to the JSON string
// OpenAI's wire format expects.
func openAITurnsToMessages(system string, history []Turn) []openAIChatMessage {
	msgs := make([]openAIChatMessage, 0, len(history)+1)
	msgs = append(msgs, openAIChatMessage{Role: "system", Content: system})
	for _, t := range history {
		switch t.Role {
		case "tool":
			msgs = append(msgs, openAIChatMessage{Role: "tool", Content: t.Content, ToolCallID: t.ToolCallID})
		case "assistant":
			m := openAIChatMessage{Role: "assistant", Content: t.Content}
			for _, c := range t.ToolCalls {
				argsJSON, _ := json.Marshal(c.Arguments)
				tc := openAIToolCall{ID: c.ID, Type: "function"}
				tc.Function.Name = c.Name
				tc.Function.Arguments = string(argsJSON)
				m.ToolCalls = append(m.ToolCalls, tc)
			}
			msgs = append(msgs, m)
		default:
			msgs = append(msgs, openAIChatMessage{Role: "user", Content: t.Content})
		}
	}
	return msgs
}

func openAITurnsToTools(tools []ToolSpec) []openAITool {
	out := make([]openAITool, len(tools))
	for i, t := range tools {
		out[i].Type = "function"
		out[i].Function.Name = t.Name
		out[i].Function.Description = t.Description
		out[i].Function.Parameters = t.Parameters
	}
	return out
}

// Chat is Summarize/ExtractActionItems's call reworked for multiple turns and
// tools: same request/response plumbing, but the message list carries the
// whole conversation (not one prompt) and a response may ask for tools
// instead of answering. See ToolCall's doc comment for why every call here
// ends up with a non-empty ID — OpenAI already assigns one, so there is
// nothing to synthesize, unlike GeminiProvider.Chat.
func (p *OpenAIProvider) Chat(ctx context.Context, system string, history []Turn, tools []ToolSpec) (ChatResult, error) {
	if p.APIKey == "" {
		return ChatResult{}, ErrNoAPIKey
	}
	reqBody, err := json.Marshal(openAIChatRequest{
		Model:    p.chatModel(),
		Messages: openAITurnsToMessages(system, history),
		Tools:    openAITurnsToTools(tools),
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
		return ChatResult{}, fmt.Errorf("openai request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return ChatResult{}, fmt.Errorf("openai response body: %w", err)
	}
	var out openAIChatResponse
	if jsonErr := json.Unmarshal(data, &out); jsonErr != nil {
		return ChatResult{}, fmt.Errorf("openai response (status %d): %w", resp.StatusCode, jsonErr)
	}
	if out.Error != nil {
		return ChatResult{}, fmt.Errorf("openai: %s", out.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return ChatResult{}, fmt.Errorf("openai: unexpected status %d", resp.StatusCode)
	}
	if len(out.Choices) == 0 {
		return ChatResult{}, fmt.Errorf("openai: empty response")
	}
	msg := out.Choices[0].Message
	if len(msg.ToolCalls) > 0 {
		calls := make([]ToolCall, len(msg.ToolCalls))
		for i, tc := range msg.ToolCalls {
			var args map[string]any
			if tc.Function.Arguments != "" {
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
					return ChatResult{}, fmt.Errorf("openai: decoding tool call arguments: %w", err)
				}
			}
			calls[i] = ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: args}
		}
		return ChatResult{ToolCalls: calls}, nil
	}
	return ChatResult{Text: strings.TrimSpace(msg.Content)}, nil
}

// ── Transcription (Whisper) ────────────────────────────────────────────────

type openAITranscriptionResponse struct {
	Text  string `json:"text"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Transcribe uploads the wav file at audioPath to the Whisper endpoint and
// returns its text. Whisper caps a single request at 25MB, which at the
// 16kHz mono rate audiorecorder.go records at (~1.92MB/min, ~115MB/hour) is
// only about 13 minutes of audio — comfortably past that cap for any real
// meeting. That's why meetingops.go records and transcribes in
// recordSegmentDuration-long chunks rather than one file for the whole
// meeting: audioPath here is always one chunk, not the whole recording.
// This package has no chunking or re-encoding logic of its own; a file
// over the cap (e.g. a transcript requested on an old single-file
// recording from before chunking existed) just fails with OpenAI's own
// "file too large" error, surfaced as-is.
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
