package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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
//
// Recording and transcribing run in fixed-length chunks
// (recordSegmentDuration), not as one pass over the whole meeting: each
// chunk closes, starts transcribing while the next one begins recording,
// and lands on the end of the meeting's Transcript as soon as it's back —
// visible in the detail pane well before the meeting ends, rather than
// only once it's over. update_meetings.go's handleSegmentClosed and
// handleSegmentTranscribed are where that handoff and the in-order flush
// actually happen.

// ── Messages ─────────────────────────────────────────────────────────────────

// recordSegmentTickMsg fires every recordSegmentDuration while a recording
// is in progress — handleSegmentClosed below is what actually acts on it,
// by way of closing the current segment once it does.
type recordSegmentTickMsg struct{}

// segmentClosedMsg reports one recorded chunk's ffmpeg process finishing —
// either because recordSegmentDuration elapsed and the next chunk needs to
// start (final=false), or because "r" asked to stop the whole recording
// (final=true).
type segmentClosedMsg struct {
	meetingID string
	index     int
	path      string
	final     bool
	err       error
}

// segmentTranscribedMsg reports one chunk's transcription finishing.
// Chunks can finish out of order (a slow network call for an earlier one
// isn't guaranteed to return before a later one's), so index and final are
// carried through for handleSegmentTranscribed's ordered flush rather than
// assuming index 0 arrives first.
type segmentTranscribedMsg struct {
	meetingID string
	index     int
	text      string
	final     bool
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

type ffmpegInstallFinishedMsg struct {
	meetingID string
	err       error
}

// segmentPipeline is one meeting's in-flight chunked-transcription
// bookkeeping, held in model.segmentPipelines keyed by meeting ID rather
// than as bare model fields: a meeting whose recording just stopped can
// still have chunks transcribing in the background after a different
// meeting's recording has already started (model.recorder has moved on to
// the new one), so each meeting's ordering state has to stay separate for
// that tail to land correctly instead of corrupting whichever meeting is
// recording now.
type segmentPipeline struct {
	// results holds finished chunks' text, keyed by index, for chunks that
	// arrived before every earlier index did.
	results map[int]segmentResult
	// appendCursor is the next index actually due to be appended to the
	// meeting's Transcript — see handleSegmentTranscribed's flush loop.
	appendCursor int
}

type segmentResult struct {
	text  string
	final bool
}

// ── Loading ──────────────────────────────────────────────────────────────────

func loadSuggestionsCmd(meetingID string) tea.Cmd {
	return func() tea.Msg {
		s, err := loadSuggestions(meetingID)
		return suggestionsLoadedMsg{meetingID: meetingID, suggestions: s, err: err}
	}
}

// ── Recording ────────────────────────────────────────────────────────────────

// cleanupOrphanedRecordings removes everything under <data>/recordings/,
// called once at startup (main.go). No recording is ever resumed across a
// restart — model.recorder is in-memory only — so anything still there is
// debris from a session that ended mid-recording without the normal
// cleanup running: a crash, a kill, a power loss. The normal paths
// (transcribeSegmentCmd, handleSegmentClosed) already delete a chunk's
// file whether transcribing it succeeded or failed; this is the backstop
// for the one case neither can reach.
func cleanupOrphanedRecordings() {
	dataDir, err := paths.Dir(paths.Data)
	if err != nil {
		return
	}
	_ = os.RemoveAll(filepath.Join(dataDir, "recordings"))
}

// recordSegmentDuration is how long each recorded chunk runs before it
// closes and starts transcribing while the next chunk keeps recording,
// rather than recording the whole meeting as one file and only starting to
// transcribe once it ends. Sized well under Whisper's 25MB request cap
// (aiprovider/openai.go) at the 16kHz mono rate audiorecorder.go records
// at (~1.92MB/min, so 5 minutes is ~9.6MB) rather than right up against
// it, and short enough that a transcript actually starts appearing well
// before a long meeting is over.
const recordSegmentDuration = 5 * time.Minute

// recordSegmentTick drives recordSegmentTickMsg once per
// recordSegmentDuration; handleMeetingsToggleRecord/handleSegmentClosed
// reschedule it for as long as a recording is in progress and let it lapse
// once it isn't (the same pattern timerTick uses for the per-second UI
// tick).
func recordSegmentTick() tea.Cmd {
	return tea.Tick(recordSegmentDuration, func(time.Time) tea.Msg {
		return recordSegmentTickMsg{}
	})
}

// segmentRecordingPath is where one chunk's audio is kept while it's being
// recorded, and afterward too if its transcription fails (a successful one
// deletes it — transcribeSegmentCmd): <data>/recordings/<meetingID>/seg-
// <NNN>.wav, a subdirectory per meeting since a long one can have a dozen
// of these alive or awaiting transcription at once.
func segmentRecordingPath(meetingID string, index int) (string, error) {
	dataDir, err := paths.Dir(paths.Data)
	if err != nil {
		return "", fmt.Errorf("resolving data directory: %w", err)
	}
	dir := filepath.Join(dataDir, "recordings", meetingID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, fmt.Sprintf("seg-%03d.wav", index)), nil
}

// startMeetingSegment begins capturing audio for one chunk of m's
// recording and updates its status. The caller is responsible for storing
// the returned *AudioRecorder on the model — StartRecording itself is fast
// (just launching ffmpeg), so this runs synchronously in the key handler
// rather than as a tea.Cmd.
func startMeetingSegment(m *meeting.Meeting, index int, ffmpegInput string) (*AudioRecorder, error) {
	path, err := segmentRecordingPath(m.ID, index)
	if err != nil {
		return nil, err
	}
	rec, err := StartRecording(path, ffmpegInput)
	if err != nil {
		return nil, err
	}
	if index == 0 {
		m.AudioPath = filepath.Dir(path)
	}
	m.Status = meeting.StatusRecording
	m.ErrorMsg = ""
	return rec, nil
}

// closeSegmentCmd asks the recorder to finish (blocking up to
// stopGraceWindow), off the Update loop — used both when "r" stops the
// whole recording (final=true) and when a chunk's time is up and the next
// one is about to start (final=false).
func closeSegmentCmd(rec *AudioRecorder, meetingID, path string, index int, final bool) tea.Cmd {
	return func() tea.Msg {
		err := rec.Stop()
		return segmentClosedMsg{meetingID: meetingID, index: index, path: path, final: final, err: err}
	}
}

// ffmpegInstallCmd hands the terminal to the install command
// ffmpegInstallCommand resolved, the same way execEditor hands it to
// $EDITOR — so sudo's password prompt, or brew's/winget's own output,
// appears directly in the terminal tjek is already running in rather than
// being hidden or run blind.
func ffmpegInstallCmd(meetingID, name string, args []string) tea.Cmd {
	c := exec.Command(name, args...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return ffmpegInstallFinishedMsg{meetingID: meetingID, err: err}
	})
}

// ── Transcription + AI pass ─────────────────────────────────────────────────

const aiRequestTimeout = 5 * time.Minute

// transcribeSegmentCmd transcribes one closed chunk and always deletes its
// audio file afterward, whether or not transcribing it succeeded — nothing
// about a meeting's recorded audio is meant to be kept once tjek is done
// with it, a failed chunk gets a placeholder rather than a retry
// (handleSegmentTranscribed), and keeping the file around wouldn't change
// that. cleanupOrphanedRecordings is the backstop for the one case this
// can't cover: the process ending before this ever runs.
func transcribeSegmentCmd(meetingID, path string, index int, final bool, keys aiprovider.Keys) tea.Cmd {
	return func() tea.Msg {
		// defer, not a call at each return site, so the file is gone on
		// every exit from here — including NewTranscriber failing before
		// a request is even made (keys.OpenAI empty; not reachable through
		// handleSegmentClosed today, which takes the no-key shortcut
		// before ever calling this, but true regardless of caller).
		defer os.Remove(path)
		tr, err := aiprovider.NewTranscriber(keys)
		if err != nil {
			return segmentTranscribedMsg{meetingID: meetingID, index: index, final: final, err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), aiRequestTimeout)
		defer cancel()
		text, err := tr.Transcribe(ctx, path)
		return segmentTranscribedMsg{meetingID: meetingID, index: index, text: text, final: final, err: err}
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
