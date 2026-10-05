package main

import (
	"strings"

	"github.com/Iliorn/tjek/aiprovider"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// aisettings.go is the Settings-tab surface for the Meetings feature's AI
// configuration: which text provider summarizes and extracts action items,
// and the three providers' API keys. All three keys are kept regardless of
// which provider is active, since transcription always needs OpenAI's — see
// aiprovider.NewTranscriber.

// cycleAIProvider steps m.aiProvider through aiprovider.ProviderNames, the
// same ‹ cycle › pattern as cycleTheme/cycleLang.
func (m *model) cycleAIProvider(dir int) {
	names := aiprovider.ProviderNames
	idx := 0
	for i, n := range names {
		if n == m.aiProvider || (m.aiProvider == "" && n == aiprovider.ProviderAnthropic) {
			idx = i
			break
		}
	}
	idx = (idx + dir + len(names)) % len(names)
	m.aiProvider = names[idx]
	m.persistSettings()
}

// updateEditAnthropicKey, updateEditOpenAIKey and updateEditGeminiKey each
// handle one inline key editor, pre-filled and masked like
// updateEditSyncToken (syncsettings.go) — clearing the field to blank removes
// the stored key.

func (m model) updateEditAnthropicKey(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			m.aiKeys.Anthropic = strings.TrimSpace(m.textInput.Value())
			m.persistSettings()
			m.mode = modeNormal
			m.textInput.EchoMode = textinput.EchoNormal
			return m, nil
		case "esc":
			m.mode = modeNormal
			m.textInput.EchoMode = textinput.EchoNormal
			return m, nil
		}
	}
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

func (m model) updateEditOpenAIKey(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			m.aiKeys.OpenAI = strings.TrimSpace(m.textInput.Value())
			m.persistSettings()
			m.mode = modeNormal
			m.textInput.EchoMode = textinput.EchoNormal
			return m, nil
		case "esc":
			m.mode = modeNormal
			m.textInput.EchoMode = textinput.EchoNormal
			return m, nil
		}
	}
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

func (m model) updateEditGeminiKey(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			m.aiKeys.Gemini = strings.TrimSpace(m.textInput.Value())
			m.persistSettings()
			m.mode = modeNormal
			m.textInput.EchoMode = textinput.EchoNormal
			return m, nil
		case "esc":
			m.mode = modeNormal
			m.textInput.EchoMode = textinput.EchoNormal
			return m, nil
		}
	}
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

// updateEditFFmpegInput handles the microphone override field. Unlike the
// three key editors it is not a secret, so EchoMode is never touched.
func (m model) updateEditFFmpegInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			m.ffmpegInput = strings.TrimSpace(m.textInput.Value())
			m.persistSettings()
			m.mode = modeNormal
			return m, nil
		case "esc":
			m.mode = modeNormal
			return m, nil
		}
	}
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}
