package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Iliorn/tjek/aiprovider"
	"github.com/Iliorn/tjek/meeting"
	"github.com/Iliorn/tjek/paths"
	"github.com/Iliorn/tjek/todo"

	tea "github.com/charmbracelet/bubbletea"
)

// meetingops.go holds the Meetings tab's business logic: the async pipeline
// (record → transcribe → summarize → extract) as tea.Cmds, and turning an
// accepted meeting.Suggestion into a real task through the same path
// update_modes.go's quick-add uses. Key handling lives in
// update_meetings.go; rendering in view_meetings.go.

// ── Messages ─────────────────────────────────────────────────────────────────

type recordingStoppedMsg struct {
	meetingID string
	err       error
}

type transcribeDoneMsg struct {
	meetingID string
	text      string
	err       error
}

type aiPassDoneMsg struct {
	meetingID   string
	summary     string
	suggestions []aiprovider.Suggestion
	err         error
}

type suggestionsLoadedMsg struct {
	meetingID   string
	suggestions []meeting.Suggestion
	err         error
}

// ── Loading ──────────────────────────────────────────────────────────────────

func loadSuggestionsCmd(meetingID string) tea.Cmd {
	return func() tea.Msg {
		s, err := loadSuggestions(meetingID)
		return suggestionsLoadedMsg{meetingID: meetingID, suggestions: s, err: err}
	}
}

// ── Recording ────────────────────────────────────────────────────────────────

// recordingPath is where a meeting's audio is kept: <data>/recordings/<id>.wav.
// A fresh directory per install, created lazily on the first recording.
func recordingPath(meetingID string) (string, error) {
	dataDir, err := paths.Dir(paths.Data)
	if err != nil {
		return "", fmt.Errorf("resolving data directory: %w", err)
	}
	dir := filepath.Join(dataDir, "recordings")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, meetingID+".wav"), nil
}

// startMeetingRecording begins capturing audio for m and updates its status.
// The caller is responsible for storing the returned *AudioRecorder on the
// model — StartRecording itself is fast (just launching ffmpeg), so this runs
// synchronously in the key handler rather than as a tea.Cmd.
func startMeetingRecording(m *meeting.Meeting, ffmpegInput string) (*AudioRecorder, error) {
	path, err := recordingPath(m.ID)
	if err != nil {
		return nil, err
	}
	rec, err := StartRecording(path, ffmpegInput)
	if err != nil {
		return nil, err
	}
	m.AudioPath = path
	m.Status = meeting.StatusRecording
	m.ErrorMsg = ""
	return rec, nil
}

// stopRecordingCmd asks the recorder to finish (blocking up to
// stopGraceWindow), off the Update loop.
func stopRecordingCmd(rec *AudioRecorder, meetingID string) tea.Cmd {
	return func() tea.Msg {
		err := rec.Stop()
		return recordingStoppedMsg{meetingID: meetingID, err: err}
	}
}

// ── Transcription + AI pass ─────────────────────────────────────────────────

const aiRequestTimeout = 5 * time.Minute

func transcribeCmd(meetingID, audioPath string, keys aiprovider.Keys) tea.Cmd {
	return func() tea.Msg {
		tr, err := aiprovider.NewTranscriber(keys)
		if err != nil {
			return transcribeDoneMsg{meetingID: meetingID, err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), aiRequestTimeout)
		defer cancel()
		text, err := tr.Transcribe(ctx, audioPath)
		return transcribeDoneMsg{meetingID: meetingID, text: text, err: err}
	}
}

// runAIPassCmd summarizes input (meeting.Meeting.Input() — Notes and
// Transcript combined) and extracts action items in one round trip (two
// sequential API calls), reporting both results together — the review
// screen needs the summary and the suggestions at once, and splitting them
// into two messages would mean the UI showing a summary with no suggestions
// yet for no reason a user could act on.
func runAIPassCmd(meetingID, input string, provider string, keys aiprovider.Keys, existingProjects, existingTags []string) tea.Cmd {
	return func() tea.Msg {
		p, err := aiprovider.New(provider, keys)
		if err != nil {
			return aiPassDoneMsg{meetingID: meetingID, err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), aiRequestTimeout)
		defer cancel()
		summary, err := p.Summarize(ctx, input)
		if err != nil {
			return aiPassDoneMsg{meetingID: meetingID, err: err}
		}
		items, err := p.ExtractActionItems(ctx, input, summary, existingProjects, existingTags)
		if err != nil {
			return aiPassDoneMsg{meetingID: meetingID, err: err}
		}
		return aiPassDoneMsg{meetingID: meetingID, summary: summary, suggestions: items}
	}
}

// ── Turning a suggestion into a task ────────────────────────────────────────

// acceptSuggestion creates a real task from s, the same way quick-add does
// (todo.New, then project/tags/priority/due applied before the first save) —
// see update_modes.go's "a" handler, which this mirrors field for field so a
// meeting-sourced task behaves identically to a typed one. t.MeetingID
// traces it back to the meeting that proposed it.
func acceptSuggestion(s meeting.Suggestion) todo.Todo {
	t := todo.New(s.Title)
	t.MeetingID = s.MeetingID
	if s.SuggestedProject != "" {
		t.Project = s.SuggestedProject
	}
	for _, tag := range s.SuggestedTags {
		t.AddTag(tag)
	}
	switch s.SuggestedPriority {
	case "h":
		t.Priority = todo.PriorityHigh
	case "l":
		t.Priority = todo.PriorityLow
	default:
		t.Priority = todo.PriorityMedium
	}
	if s.SuggestedDue != "" {
		if d, err := parseDueDate(s.SuggestedDue); err == nil {
			t.DueDate = d
		}
		// An unparseable hint is dropped silently rather than failing
		// acceptance outright — the task is still worth having without a due
		// date, and the suggestion's text is visible on the review row before
		// this runs, so a bad date hint was already something to notice there.
	}
	return t
}
