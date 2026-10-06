package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Iliorn/tjek/aiprovider"
	"github.com/Iliorn/tjek/meeting"
	"github.com/Iliorn/tjek/todo"
)

// update_meetings_test.go scripts the Meetings tab the same way
// update_keyscript_test.go does for the rest of the app: real Update calls,
// asserting the store/review bookkeeping rather than rendering. The record →
// transcribe → summarize pipeline's network calls are simulated by delivering
// the messages those tea.Cmds would produce directly, which is also what
// exercises handleRecordingStopped/handleTranscribeDone/handleAIPassDone
// without a real ffmpeg or API call.

// TestScriptMeetingNotesAreIndependentOfTranscript asserts the thing that
// prompted the Notes/Transcript split: a meeting with only hand-typed notes
// (no recording at all) can still run the AI pass, and writing one field
// never touches the other. $EDITOR itself isn't driven here — Notes is set
// directly, the same stand-in handleEditorFinished's own round trip ends
// with (see TestScriptAddMeetingAndRunReview's Transcript for the existing
// precedent).
func TestScriptMeetingNotesAreIndependentOfTranscript(t *testing.T) {
	m := modelWithTasks(t)
	m.tab = tabMeetings
	m = script(t, m, "a", "1:1 with Alice", "enter")
	mt := m.meetings[0]

	m = sendKey(t, m, "enter") // open it
	if m.openMeetingID != mt.ID {
		t.Fatalf("openMeetingID = %q, want %q", m.openMeetingID, mt.ID)
	}

	mt.Notes = "Remember to ask about the Q4 budget"
	if err := saveMeeting(mt); err != nil {
		t.Fatal(err)
	}
	if mt.Transcript != "" {
		t.Fatalf("writing Notes touched Transcript: %q", mt.Transcript)
	}
	if !mt.CanRunAI() {
		t.Fatal("CanRunAI() = false for a meeting with only Notes (no recording at all)")
	}

	// "g" from the detail pane must actually reach handleMeetingsRunAI —
	// this is exactly the path TestScriptAddMeetingAndRunReview's fix
	// (updateMeetingsDetail) made reachable in the first place.
	m = sendKey(t, m, "g")
	if mt.Status != meeting.StatusSummarizing {
		t.Fatalf("after 'g' on a notes-only meeting: status = %v, want StatusSummarizing", mt.Status)
	}

	// Now also set a transcript (as if a later recording was transcribed)
	// and confirm Notes survives untouched.
	mt.Transcript = "Alice said the budget is already approved."
	if err := saveMeeting(mt); err != nil {
		t.Fatal(err)
	}
	if mt.Notes != "Remember to ask about the Q4 budget" {
		t.Fatalf("writing Transcript touched Notes: %q", mt.Notes)
	}
	wantInput := "## My notes\nRemember to ask about the Q4 budget\n\n## Transcript\nAlice said the budget is already approved."
	if got := mt.Input(); got != wantInput {
		t.Fatalf("Input() = %q, want %q", got, wantInput)
	}
}

func TestScriptAddMeetingAndRunReview(t *testing.T) {
	m := modelWithTasks(t)
	m.tab = tabMeetings

	// Add.
	m = sendKey(t, m, "a")
	if m.mode != modeAddMeeting {
		t.Fatalf("after 'a' on Meetings: mode = %v, want modeAddMeeting", m.mode)
	}
	m = script(t, m, "Sprint planning", "enter")
	if m.mode != modeNormal {
		t.Fatalf("after enter: mode = %v, want modeNormal", m.mode)
	}
	if len(m.meetings) != 1 {
		t.Fatalf("meetings = %d, want 1", len(m.meetings))
	}
	mt := m.meetings[0]
	if mt.Title != "Sprint planning" || mt.Status != meeting.StatusDraft {
		t.Fatalf("created meeting = %+v", mt)
	}

	// Open it, type a transcript directly (bypassing $EDITOR, which this
	// suite cannot drive), and run the AI pass.
	m = sendKey(t, m, "enter")
	if m.pane != paneDetail || m.openMeetingID != mt.ID {
		t.Fatalf("after enter on the list: pane=%v openMeetingID=%q", m.pane, m.openMeetingID)
	}
	mt.Transcript = "Alice will email the vendor. Bob will update the roadmap doc."
	if err := saveMeeting(mt); err != nil {
		t.Fatal(err)
	}

	m = script(t, m, "g")
	if mt.Status != meeting.StatusSummarizing {
		t.Fatalf("after 'g': meeting status = %v, want StatusSummarizing", mt.Status)
	}

	// Simulate the AI pass completing — two action items.
	next, _ := m.Update(aiPassDoneMsg{
		meetingID: mt.ID,
		summary:   "Alice and Bob have follow-ups.",
		suggestions: []aiprovider.Suggestion{
			{Title: "Email the vendor", Priority: "h"},
			{Title: "Update the roadmap doc", Priority: "m"},
		},
	})
	m = next.(model)
	if mt.Status != meeting.StatusReady {
		t.Fatalf("after AI pass: meeting status = %v, want StatusReady", mt.Status)
	}
	if len(m.meetingSuggestions) != 2 {
		t.Fatalf("meetingSuggestions = %d, want 2", len(m.meetingSuggestions))
	}
	if m.meetingReviewCursor != 0 {
		t.Fatalf("meetingReviewCursor = %d, want 0", m.meetingReviewCursor)
	}

	tasksBefore := m.len()

	// Accept the first.
	m = sendKey(t, m, "y")
	if m.len() != tasksBefore+1 {
		t.Fatalf("after accept: store has %d tasks, want %d", m.len(), tasksBefore+1)
	}
	created := m.currentTodo()
	if created == nil {
		// currentTodo tracks the Tasks-tab cursor, not Meetings' — look it up
		// by title instead.
		for _, tk := range m.allTodos() {
			if tk.Title == "Email the vendor" {
				created = tk
			}
		}
	}
	if created == nil || created.Priority != todo.PriorityHigh || created.MeetingID != mt.ID {
		t.Fatalf("created task = %+v", created)
	}
	if !m.meetingSuggestions[0].Resolved || !m.meetingSuggestions[0].Accepted {
		t.Fatalf("suggestion[0] = %+v, want resolved+accepted", m.meetingSuggestions[0])
	}
	if m.meetingReviewCursor != 1 {
		t.Fatalf("meetingReviewCursor = %d, want 1 (advanced to the next unresolved)", m.meetingReviewCursor)
	}

	// Reject the second — meeting should finish review.
	tasksAfterAccept := m.len()
	m = sendKey(t, m, "x")
	if m.len() != tasksAfterAccept {
		t.Fatalf("reject created a task: store has %d, want %d", m.len(), tasksAfterAccept)
	}
	if !m.meetingSuggestions[1].Resolved || m.meetingSuggestions[1].Accepted {
		t.Fatalf("suggestion[1] = %+v, want resolved, not accepted", m.meetingSuggestions[1])
	}
	if mt.Status != meeting.StatusReviewed {
		t.Fatalf("meeting status = %v, want StatusReviewed once every suggestion is resolved", mt.Status)
	}
	if m.meetingReviewCursor != -1 {
		t.Fatalf("meetingReviewCursor = %d, want -1 once review is finished", m.meetingReviewCursor)
	}
}

func TestScriptDeleteMeetingAsksToConfirm(t *testing.T) {
	m := modelWithTasks(t)
	m.tab = tabMeetings
	m = script(t, m, "a", "Standup", "enter")
	if len(m.meetings) != 1 {
		t.Fatalf("meetings = %d, want 1", len(m.meetings))
	}

	m = sendKey(t, m, "x")
	if m.mode != modeConfirm {
		t.Fatalf("after 'x' on the list: mode = %v, want modeConfirm", m.mode)
	}
	m = sendKey(t, m, "y")
	if len(m.meetings) != 0 {
		t.Fatalf("after confirming delete: meetings = %d, want 0", len(m.meetings))
	}
}

// TestScriptEditMeetingNotesCtrlSAndEscBothSave exercises the in-app
// textarea editor that replaced the $EDITOR round trip for Notes/Transcript:
// "n" opens it seeded from the current value, and both ctrl+s and esc commit
// the edit and return to modeNormal — esc must never discard, so a reflex
// esc can't cost someone a long note.
func TestScriptEditMeetingNotesCtrlSAndEscBothSave(t *testing.T) {
	m := modelWithTasks(t)
	m.tab = tabMeetings
	m = script(t, m, "a", "1:1 with Alice", "enter")
	mt := m.meetings[0]
	m = sendKey(t, m, "enter") // open the detail pane

	m = sendKey(t, m, "n")
	if m.mode != modeEditMeetingText {
		t.Fatalf("after 'n': mode = %v, want modeEditMeetingText", m.mode)
	}
	if m.meetingEditID != mt.ID || m.meetingEditField != "notes" {
		t.Fatalf("meetingEditID=%q meetingEditField=%q, want %q/notes", m.meetingEditID, m.meetingEditField, mt.ID)
	}
	if !m.meetingTextarea.Focused() {
		t.Fatal("meetingTextarea is not focused after 'n'")
	}

	// Both key-hint lines must survive the fullscreen layout's line budget —
	// meetingEditChromeLines undercounting by even one silently clips the
	// second line instead of the harmless trailing blank it's meant to.
	rendered := m.View()
	if !strings.Contains(rendered, "save and exit") {
		t.Error("edit screen is missing the ctrl+s/esc hint line")
	}
	if !strings.Contains(rendered, "move by word") {
		t.Error("edit screen is missing the word-jump/clear-line hint line")
	}

	m = script(t, m, "Ask about the Q4 budget", "ctrl+s")
	if m.mode != modeNormal {
		t.Fatalf("after ctrl+s: mode = %v, want modeNormal", m.mode)
	}
	if mt.Notes != "Ask about the Q4 budget" {
		t.Fatalf("mt.Notes = %q, want %q", mt.Notes, "Ask about the Q4 budget")
	}
	if mt.Transcript != "" {
		t.Fatalf("saving Notes touched Transcript: %q", mt.Transcript)
	}

	// Re-open and edit again, this time exiting with esc: it must save just
	// like ctrl+s did, not discard.
	m = sendKey(t, m, "n")
	m = script(t, m, " — more", "esc")
	if m.mode != modeNormal {
		t.Fatalf("after esc: mode = %v, want modeNormal", m.mode)
	}
	if mt.Notes != "Ask about the Q4 budget — more" {
		t.Fatalf("esc should save like ctrl+s, got mt.Notes = %q", mt.Notes)
	}
}

// TestScriptRecordWithoutFFmpegOffersToInstallIt covers the confirm prompt
// only — not confirmOnYes itself, which would actually exec a package
// manager. This relies on ffmpeg genuinely being absent, the same
// environment TestStartRecordingNoFFmpeg (audiorecorder_test.go) depends on.
func TestScriptRecordWithoutFFmpegOffersToInstallIt(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		t.Skip("ffmpeg is installed in this environment; the not-found path can't be exercised here")
	}

	m := modelWithTasks(t)
	m.tab = tabMeetings
	m = script(t, m, "a", "Standup", "enter")
	mt := m.meetings[0]
	m = sendKey(t, m, "enter") // open the detail pane

	m = sendKey(t, m, "r")
	if m.mode != modeConfirm {
		t.Fatalf("after 'r' with no ffmpeg: mode = %v, want modeConfirm", m.mode)
	}
	if !strings.Contains(m.confirmMsg, "ffmpeg") {
		t.Fatalf("confirmMsg = %q, want it to mention ffmpeg", m.confirmMsg)
	}
	if m.pendingFFmpegInstallMeetingID != mt.ID {
		t.Fatalf("pendingFFmpegInstallMeetingID = %q, want %q", m.pendingFFmpegInstallMeetingID, mt.ID)
	}
	if m.confirmOnYes == nil {
		t.Fatal("confirmOnYes is nil; 'y' would do nothing")
	}
	if mt.Status == meeting.StatusRecording {
		t.Fatal("recording must not have started before the install prompt is answered")
	}

	// "n" backs out through the generic modeConfirm decline path, which
	// resets mode and confirmOnYes but — like pendingDeleteID elsewhere —
	// leaves pendingFFmpegInstallMeetingID set; it's inert until the next
	// promptInstallFFmpeg overwrites it, and nothing reads it outside that.
	m = sendKey(t, m, "n")
	if m.mode != modeNormal {
		t.Fatalf("after 'n': mode = %v, want modeNormal", m.mode)
	}
	if m.confirmOnYes != nil {
		t.Fatal("confirmOnYes should be cleared after declining")
	}
}

// TestHandleFFmpegInstallFinished covers the two outcomes that don't require
// an actual package manager run: the install command itself erroring, and
// it exiting cleanly without ffmpeg actually ending up on PATH (a declined
// sudo prompt or agreement prompt can do this without a non-zero exit).
// The success-and-resume path needs ffmpeg to really be installed partway
// through the test and is checked by hand instead, same as
// TestStartRecordingNoFFmpeg's note on the record→Stop round trip.
func TestHandleFFmpegInstallFinished(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		t.Skip("ffmpeg is installed in this environment; the not-found-after-install path can't be exercised here")
	}

	m := modelWithTasks(t)
	m.tab = tabMeetings
	m = script(t, m, "a", "Standup", "enter")
	mt := m.meetings[0]
	m = sendKey(t, m, "enter")

	next, _ := m.Update(ffmpegInstallFinishedMsg{meetingID: mt.ID, err: errors.New("exit status 1")})
	m = next.(model)
	if !strings.Contains(m.err, "failed") {
		t.Fatalf("err flash = %q, want it to mention the install failing", m.err)
	}
	if mt.Status == meeting.StatusRecording {
		t.Fatal("a failed install must not have started recording")
	}

	next, _ = m.Update(ffmpegInstallFinishedMsg{meetingID: mt.ID})
	m = next.(model)
	if !strings.Contains(m.err, "PATH") {
		t.Fatalf("err flash = %q, want it to say ffmpeg still isn't on PATH", m.err)
	}
	if mt.Status == meeting.StatusRecording {
		t.Fatal("recording must not start when ffmpeg still isn't found after the install")
	}
}

// TestNeedsSecondTickIncludesRecording guards the fix for the status line's
// elapsed time looking frozen and then jumping several seconds at once: it
// only redraws when something triggers a render, so timerTick's once-a-
// second loop (model.go) has to keep running while a recording is active,
// not only while a task timer is. This can't go through the real "r" key —
// StartRecording needs a real ffmpeg on PATH, which this environment
// doesn't have (see TestStartRecordingNoFFmpeg) — so it checks the
// condition directly against a stand-in *AudioRecorder.
func TestNeedsSecondTickIncludesRecording(t *testing.T) {
	m := modelWithTasks(t)
	if m.needsSecondTick() {
		t.Fatal("needsSecondTick() = true with nothing running")
	}
	m.recorder = &AudioRecorder{}
	if !m.needsSecondTick() {
		t.Fatal("needsSecondTick() = false while a recording is active, want true")
	}
	m.recorder = nil
	if m.needsSecondTick() {
		t.Fatal("needsSecondTick() = true after the recording cleared, want false")
	}
}

// The remaining tests in this file cover the chunked recording
// pipeline (meetingops.go): recording and transcribing in
// recordSegmentDuration-long chunks instead of one pass over the whole
// meeting. The happy-path rollover itself needs a real ffmpeg to start the
// next chunk (see TestStartRecordingNoFFmpeg's note on why this
// environment can't run it); handleSegmentTranscribed's ordering, the
// no-key shortcut, and the per-chunk-failure placeholder need neither
// ffmpeg nor a network call, so those are driven directly by delivering
// the messages the real pipeline would produce — the same technique
// TestScriptAddMeetingAndRunReview uses for the AI pass.

// TestScriptSegmentsFlushInOrderDespiteArrivingOutOfOrder is the core
// correctness property of the chunked pipeline: a slow network call for an
// earlier chunk isn't guaranteed to return before a later one's, and the
// transcript must still read in recording order regardless.
func TestScriptSegmentsFlushInOrderDespiteArrivingOutOfOrder(t *testing.T) {
	m := modelWithTasks(t)
	m.tab = tabMeetings
	m = script(t, m, "a", "Weekly sync", "enter")
	mt := m.meetings[0]
	m = sendKey(t, m, "enter") // open the detail pane
	m.aiKeys.OpenAI = "test-key"
	mt.Status = meeting.StatusRecording

	// Segment 1 finishes before segment 0 does.
	next, _ := m.Update(segmentTranscribedMsg{meetingID: mt.ID, index: 1, text: "second chunk"})
	m = next.(model)
	if mt.Transcript != "" {
		t.Fatalf("segment 1 landed before segment 0: Transcript = %q", mt.Transcript)
	}

	next, _ = m.Update(segmentTranscribedMsg{meetingID: mt.ID, index: 0, text: "first chunk"})
	m = next.(model)
	if mt.Transcript != "first chunk second chunk" {
		t.Fatalf("Transcript = %q, want both segments flushed in order", mt.Transcript)
	}
	if mt.Status != meeting.StatusRecording {
		t.Fatalf("Status = %v, want still StatusRecording before the final chunk", mt.Status)
	}

	// Segment 2, final: completes the recording and kicks off the AI pass —
	// this is also the part of the pipeline that makes the transcript
	// "visible as it's transcribed" rather than only once the meeting ends.
	next, cmd := m.Update(segmentTranscribedMsg{meetingID: mt.ID, index: 2, text: "third chunk", final: true})
	m = next.(model)
	if mt.Transcript != "first chunk second chunk third chunk" {
		t.Fatalf("Transcript = %q after the final segment", mt.Transcript)
	}
	if mt.Status != meeting.StatusSummarizing {
		t.Fatalf("Status = %v, want StatusSummarizing once every segment is in", mt.Status)
	}
	if cmd == nil {
		t.Fatal("the final segment should return the AI-pass command")
	}
	if len(m.segmentPipelines) != 0 {
		t.Fatalf("segmentPipelines = %v, want the finished meeting's entry removed", m.segmentPipelines)
	}
}

// TestScriptSegmentTranscriptionFailureInsertsPlaceholderAndContinues
// covers why a failed chunk doesn't abort the whole meeting: a dropped
// network call for one five-minute chunk shouldn't cost the rest of an
// hour-long recording the way a single-file failure used to.
func TestScriptSegmentTranscriptionFailureInsertsPlaceholderAndContinues(t *testing.T) {
	m := modelWithTasks(t)
	m.tab = tabMeetings
	m = script(t, m, "a", "Standup", "enter")
	mt := m.meetings[0]
	m = sendKey(t, m, "enter")
	m.aiKeys.OpenAI = "test-key"
	mt.Status = meeting.StatusRecording

	next, _ := m.Update(segmentTranscribedMsg{meetingID: mt.ID, index: 0, text: "all good", err: errors.New("network blip")})
	m = next.(model)
	if !strings.Contains(mt.Transcript, "failed") {
		t.Fatalf("Transcript = %q, want a placeholder for the failed chunk", mt.Transcript)
	}
	if mt.Status == meeting.StatusError {
		t.Fatal("one failed chunk must not abort the whole meeting")
	}

	next, _ = m.Update(segmentTranscribedMsg{meetingID: mt.ID, index: 1, text: "second chunk", final: true})
	m = next.(model)
	if !strings.Contains(mt.Transcript, "second chunk") {
		t.Fatalf("Transcript = %q, want the later chunk to still land", mt.Transcript)
	}
	if mt.Status != meeting.StatusSummarizing {
		t.Fatalf("Status = %v, want StatusSummarizing once the recording finishes despite the earlier failure", mt.Status)
	}
}

// TestScriptSegmentWithoutOpenAIKeyLandsOnDraft matches the single-file
// pipeline's behavior for a meeting recorded with no OpenAI key configured:
// no transcription is attempted, nothing is inserted into Transcript, and
// the meeting lands on StatusDraft with a hint instead of silently stuck
// or treated as an error.
func TestScriptSegmentWithoutOpenAIKeyLandsOnDraft(t *testing.T) {
	m := modelWithTasks(t)
	m.tab = tabMeetings
	m = script(t, m, "a", "Standup", "enter")
	mt := m.meetings[0]
	m = sendKey(t, m, "enter")
	mt.Status = meeting.StatusRecording
	// m.aiKeys.OpenAI left empty.

	next, _ := m.Update(segmentTranscribedMsg{meetingID: mt.ID, index: 0, final: true})
	m = next.(model)
	if mt.Transcript != "" {
		t.Fatalf("Transcript = %q, want nothing inserted with no key configured", mt.Transcript)
	}
	if mt.Status != meeting.StatusDraft {
		t.Fatalf("Status = %v, want StatusDraft with no OpenAI key configured", mt.Status)
	}
	if !strings.Contains(m.err, "API key") {
		t.Fatalf("err flash = %q, want it to mention the missing API key", m.err)
	}
}

// TestRecordSegmentTickIgnoredOnceRecordingHasStopped guards the stale-tick
// path: tea.Tick has no cancel handle, so a rollover tick scheduled before
// "r" stopped the recording can still arrive afterward, and must be a
// no-op rather than acting on a recorder that's gone.
func TestRecordSegmentTickIgnoredOnceRecordingHasStopped(t *testing.T) {
	m := modelWithTasks(t)
	m.tab = tabMeetings
	m = script(t, m, "a", "Standup", "enter")
	mt := m.meetings[0]
	m.recorder = nil // the recording has already fully stopped

	next, cmd := m.Update(recordSegmentTickMsg{})
	m = next.(model)
	if cmd != nil {
		t.Fatal("a stale recordSegmentTick must not schedule a close")
	}
	if mt.Status == meeting.StatusError {
		t.Fatal("a stale tick must not touch the meeting at all")
	}
}

// TestScriptSegmentClosedErrorStopsTheRecording covers handleSegmentClosed's
// own failure path: ffmpeg itself erroring on a chunk (as opposed to a
// transcription call failing, which handleSegmentTranscribed tolerates)
// is treated as a real recording failure.
func TestScriptSegmentClosedErrorStopsTheRecording(t *testing.T) {
	m := modelWithTasks(t)
	m.tab = tabMeetings
	m = script(t, m, "a", "Standup", "enter")
	mt := m.meetings[0]
	m = sendKey(t, m, "enter")
	m.recorder = &AudioRecorder{}
	m.recordingMeetingID = mt.ID
	mt.Status = meeting.StatusRecording

	next, _ := m.Update(segmentClosedMsg{meetingID: mt.ID, index: 0, final: true, err: errors.New("ffmpeg crashed")})
	m = next.(model)
	if mt.Status != meeting.StatusError {
		t.Fatalf("Status = %v, want StatusError", mt.Status)
	}
	if m.recorder != nil {
		t.Fatal("recorder should be cleared after a recording error")
	}
}

// TestScriptSegmentRolloverWithoutFFmpegSurfacesAnError exercises
// handleSegmentClosed's "could not start the next chunk" error path — the
// happy-path rollover itself needs a real ffmpeg (see
// TestStartRecordingNoFFmpeg's note), which this environment doesn't have.
func TestScriptSegmentRolloverWithoutFFmpegSurfacesAnError(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		t.Skip("ffmpeg is installed in this environment; the not-found rollover path can't be exercised here")
	}
	m := modelWithTasks(t)
	m.tab = tabMeetings
	m = script(t, m, "a", "Standup", "enter")
	mt := m.meetings[0]
	m = sendKey(t, m, "enter")
	mt.Status = meeting.StatusRecording

	path, err := segmentRecordingPath(mt.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	next, _ := m.Update(segmentClosedMsg{meetingID: mt.ID, index: 0, path: path, final: false})
	m = next.(model)
	if mt.Status != meeting.StatusError {
		t.Fatalf("Status = %v, want StatusError once the next chunk can't start", mt.Status)
	}
}

// TestCleanupOrphanedRecordingsRemovesLeftoverAudio guards the crash-
// recovery backstop: recording state is never resumed across a restart,
// so anything still under <data>/recordings/ at the next startup is
// debris from a session that ended mid-recording without the normal
// per-chunk cleanup ever running, and must not survive into the next one.
func TestCleanupOrphanedRecordingsRemovesLeftoverAudio(t *testing.T) {
	setTestHome(t, t.TempDir())
	path, err := segmentRecordingPath("some-meeting-id", 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("fake audio"), 0o644); err != nil {
		t.Fatal(err)
	}

	cleanupOrphanedRecordings()

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("leftover recording should be gone, stat err = %v", err)
	}
}

// TestScriptSegmentClosedSkipsNoKeyShortcutWhenLocalWhisperEnabled covers
// handleSegmentClosed's routing decision: with local whisper on, there is
// something to transcribe with even though m.aiKeys.OpenAI is empty, so
// the no-key shortcut (straight to StatusDraft, no transcription attempt)
// must not fire.
func TestScriptSegmentClosedSkipsNoKeyShortcutWhenLocalWhisperEnabled(t *testing.T) {
	m := modelWithTasks(t)
	m.tab = tabMeetings
	m = script(t, m, "a", "Standup", "enter")
	mt := m.meetings[0]
	m = sendKey(t, m, "enter")
	mt.Status = meeting.StatusRecording
	m.useLocalWhisper = true
	// m.aiKeys.OpenAI left empty on purpose.

	path, err := segmentRecordingPath(mt.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("fake audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	next, cmd := m.Update(segmentClosedMsg{meetingID: mt.ID, index: 0, path: path, final: true})
	m = next.(model)
	if cmd == nil {
		t.Fatal("expected a command attempting transcription, not the no-key shortcut")
	}
	if mt.Status == meeting.StatusDraft {
		t.Fatal("landed on StatusDraft synchronously — took the no-key shortcut instead of attempting local transcription")
	}
}

// TestTranscribeSegmentCmdLocalWhisperNotFound mirrors
// TestScriptRecordWithoutFFmpegOffersToInstallIt's "real not-found path"
// approach for whisper-cli: nothing else here can run without it actually
// installed, which this dev environment doesn't have.
func TestTranscribeSegmentCmdLocalWhisperNotFound(t *testing.T) {
	if _, err := exec.LookPath("whisper-cli"); err == nil {
		t.Skip("whisper-cli is installed in this environment; the not-found path can't be exercised here")
	}
	setTestHome(t, t.TempDir())
	path := filepath.Join(t.TempDir(), "seg-000.wav")
	if err := os.WriteFile(path, []byte("fake audio"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := transcriptionConfig{useLocal: true}
	msg := transcribeSegmentCmd("meeting-1", path, 0, true, cfg)()
	tm, ok := msg.(segmentTranscribedMsg)
	if !ok {
		t.Fatalf("msg = %T, want segmentTranscribedMsg", msg)
	}
	if tm.err == nil || !strings.Contains(tm.err.Error(), "whisper-cli not found") {
		t.Fatalf("err = %v, want a whisper-cli-not-found error", tm.err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatal("audio file should be deleted even when local whisper fails to resolve a binary")
	}
}

// TestTranscribeSegmentCmdLocalWhisperModelNotDownloaded covers the other
// local-whisper failure mode: a resolvable binary (an override, so it
// doesn't depend on what's installed) but no model file at the fixed path
// — setTestHome gives this test an isolated <data>/whisper/ with nothing
// in it.
func TestTranscribeSegmentCmdLocalWhisperModelNotDownloaded(t *testing.T) {
	setTestHome(t, t.TempDir())
	fakeBin := filepath.Join(t.TempDir(), "whisper-cli")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "seg-000.wav")
	if err := os.WriteFile(path, []byte("fake audio"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := transcriptionConfig{useLocal: true, binOverride: fakeBin}
	msg := transcribeSegmentCmd("meeting-1", path, 0, true, cfg)()
	tm, ok := msg.(segmentTranscribedMsg)
	if !ok {
		t.Fatalf("msg = %T, want segmentTranscribedMsg", msg)
	}
	if tm.err == nil || !strings.Contains(tm.err.Error(), "model not downloaded") {
		t.Fatalf("err = %v, want a model-not-downloaded error", tm.err)
	}
}

// TestTranscribeSegmentCmdLocalWhisperSuccess is the happy path, with a
// fake whisper-cli standing in for the real one — the same "don't require
// the real external tool" approach aiprovider/localwhisper_test.go uses.
func TestTranscribeSegmentCmdLocalWhisperSuccess(t *testing.T) {
	setTestHome(t, t.TempDir())
	modelPath, err := whisperModelPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modelPath, []byte("fake model"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeBin := filepath.Join(t.TempDir(), "whisper-cli")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\necho transcribed text\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	audioPath := filepath.Join(t.TempDir(), "seg-000.wav")
	if err := os.WriteFile(audioPath, []byte("fake audio"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := transcriptionConfig{useLocal: true, binOverride: fakeBin, language: "da"}
	msg := transcribeSegmentCmd("meeting-1", audioPath, 0, true, cfg)()
	tm, ok := msg.(segmentTranscribedMsg)
	if !ok {
		t.Fatalf("msg = %T, want segmentTranscribedMsg", msg)
	}
	if tm.err != nil {
		t.Fatal(tm.err)
	}
	if tm.text != "transcribed text" {
		t.Fatalf("text = %q", tm.text)
	}
	if _, statErr := os.Stat(audioPath); !os.IsNotExist(statErr) {
		t.Fatal("audio file should be deleted after a successful transcription")
	}
}
