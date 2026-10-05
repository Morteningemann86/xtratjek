package main

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/Iliorn/tjek/meeting"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// update_meetings.go is the Meetings tab's key handling and message
// wiring — woven into the existing shared handlers (updateList's switch,
// handleListEnter, handleListDelete, moveCursorUp/Down, handleEditorFinished)
// the same way every other tab is, plus a few genuinely new entry points
// (record, run AI, accept/reject a suggestion) that don't exist anywhere
// else to share. The async pipeline's tea.Cmd builders live in
// meetingops.go; rendering in view_meetings.go.

// updateMeetingsDetail handles every normal-mode key while a meeting is open
// (m.pane == paneDetail on the Meetings tab) — recording, generating, and
// reviewing action items. It is wired in at the same point updateDetail is
// (dispatch's pane switch), not inside updateList/updateDetail themselves:
// those two are Tasks-shaped (m.currentTodo(), m.detail's field cursor) and
// have no Meetings equivalent, so this is a third, independent, self-
// contained handler the same way updateDetail already is relative to
// updateList — replicating the handful of global keys (q/?/ctrl+k/u/tab
// switching) each of those already replicates independently rather than
// sharing them, for the same reason: the modeNormal key switch in Bubble Tea
// is one per entry point, not one shared dispatch table.
func (m model) updateMeetingsDetail(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "q":
		m.flushPendingWrites()
		m.closeWatcher()
		return m, tea.Quit
	case "?":
		m.mode = modeHelp
		return m, nil
	case "ctrl+k":
		return m, m.openPalette()
	case "u":
		return m, m.performUndo()
	case "1", "2", "3", "4", "5", "6", "7", "8":
		if t, ok := m.tabForNumberKey(key.String()); ok {
			m.switchTab(t)
		}
		return m, nil
	case "tab":
		m.switchTab(m.boardCfg.nextTab(m.tab, 1))
		return m, nil
	case "shift+tab":
		m.switchTab(m.boardCfg.nextTab(m.tab, -1))
		return m, nil
	case "esc":
		m.popFocus()
		return m, nil
	case "up", "k":
		m.moveMeetingsCursor(-1)
		return m, nil
	case "down", "j":
		m.moveMeetingsCursor(1)
		return m, nil
	case "enter":
		return m.handleMeetingsEnter()
	case "y":
		return m.acceptFocusedSuggestion()
	case "x", "delete":
		return m.handleMeetingsDeleteOrReject()
	case "e":
		cmd := m.startEditFocusedSuggestion()
		return m, cmd
	case "r":
		return m.handleMeetingsToggleRecord()
	case "g":
		return m.handleMeetingsRunAI()
	case "n":
		return m, m.startEditMeetingText("notes")
	case "T":
		return m, m.startEditMeetingText("transcript")
	}
	return m, nil
}

// ── Lookups ──────────────────────────────────────────────────────────────────

// currentMeeting is the list row under the cursor.
func (m model) currentMeeting() *meeting.Meeting {
	if m.cursor < 0 || m.cursor >= len(m.meetings) {
		return nil
	}
	return m.meetings[m.cursor]
}

func (m model) meetingByID(id string) *meeting.Meeting {
	for _, mt := range m.meetings {
		if mt.ID == id {
			return mt
		}
	}
	return nil
}

// meetingForEditorTarget is "the meeting this keypress is about": the open
// one in the detail pane, or the one under the cursor in the list.
func (m model) meetingForEditorTarget() *meeting.Meeting {
	if m.pane == paneDetail && m.openMeetingID != "" {
		return m.meetingByID(m.openMeetingID)
	}
	return m.currentMeeting()
}

// ── Navigation ───────────────────────────────────────────────────────────────

// moveMeetingsCursor is moveCursorUp/Down's Meetings-tab case: it steps the
// suggestion-review cursor while a meeting is open with suggestions loaded,
// and the meeting list otherwise — the same split every other tab's detail
// vs. list navigation uses.
func (m *model) moveMeetingsCursor(dir int) {
	if m.pane == paneDetail {
		if n := len(m.meetingSuggestions); n > 0 {
			m.meetingReviewCursor = ((m.meetingReviewCursor+dir)%n + n) % n
		}
		return
	}
	if n := len(m.meetings); n > 0 {
		m.cursor = (m.cursor + dir + n) % n
	}
}

// ── Enter ────────────────────────────────────────────────────────────────────

func (m model) handleMeetingsEnter() (tea.Model, tea.Cmd) {
	if m.pane == paneList {
		mt := m.currentMeeting()
		if mt == nil {
			return m, nil
		}
		m.openMeetingID = mt.ID
		m.pane = paneDetail
		m.meetingReviewCursor = -1
		m.meetingSuggestions = nil
		m.pushFocus(stateDetailPane)
		if mt.Status == meeting.StatusReady || mt.Status == meeting.StatusReviewed {
			return m, loadSuggestionsCmd(mt.ID)
		}
		return m, nil
	}
	if m.meetingReviewCursor >= 0 && m.meetingReviewCursor < len(m.meetingSuggestions) {
		return m.acceptFocusedSuggestion()
	}
	return m, nil
}

// ── Add ──────────────────────────────────────────────────────────────────────

// updateAddMeeting handles the title prompt opened by "a" on the Meetings
// tab. A bare title is enough to create a draft meeting; notes/recording
// come after, from the detail pane.
func (m model) updateAddMeeting(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			title := strings.TrimSpace(m.textInput.Value())
			m.mode = modeNormal
			if title == "" {
				return m, nil
			}
			mt := meeting.New(title)
			if err := saveMeeting(&mt); err != nil {
				m.flashError(fmt.Sprintf(tr("Error saving meeting: %v"), err))
				return m, clearErrAfter()
			}
			m.meetings = append([]*meeting.Meeting{&mt}, m.meetings...)
			m.cursor = 0
			m.flashSuccess(tr("Meeting added"))
			return m, clearErrAfter()
		case "esc":
			m.mode = modeNormal
			return m, nil
		}
	}
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

// ── Recording ────────────────────────────────────────────────────────────────

func (m model) handleMeetingsToggleRecord() (tea.Model, tea.Cmd) {
	if m.recorder != nil {
		rec, id, index := m.recorder, m.recordingMeetingID, m.recordSegmentIndex
		m.recorder = nil
		m.recordingMeetingID = ""
		path, err := segmentRecordingPath(id, index)
		if err != nil {
			m.flashError(fmt.Sprintf(tr("Error stopping recording: %v"), err))
			return m, clearErrAfter()
		}
		if mt := m.meetingByID(id); mt != nil {
			mt.Status = meeting.StatusTranscribing
		}
		m.flashInfo(tr("Stopping recording…"))
		return m, tea.Batch(clearErrAfter(), closeSegmentCmd(rec, id, path, index, true))
	}
	mt := m.meetingForEditorTarget()
	if mt == nil || !mt.CanRecord() {
		return m, nil
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return m.promptInstallFFmpeg(mt.ID)
	}
	return m.startRecordingFor(mt)
}

// startRecordingFor launches the first chunk of a new recording for mt.
// Split out of handleMeetingsToggleRecord so handleFFmpegInstallFinished
// can resume the same "r" press automatically once an install it triggered
// succeeds, without the user needing to press r a second time.
func (m model) startRecordingFor(mt *meeting.Meeting) (tea.Model, tea.Cmd) {
	rec, err := startMeetingSegment(mt, 0, m.ffmpegInput)
	if err != nil {
		m.flashError(fmt.Sprintf(tr("Could not start recording: %v"), err))
		return m, clearErrAfter()
	}
	if err := saveMeeting(mt); err != nil {
		m.flashError(fmt.Sprintf(tr("Error saving meeting: %v"), err))
	}
	m.recorder = rec
	m.recordingMeetingID = mt.ID
	m.recordStart = time.Now()
	m.recordSegmentIndex = 0
	if m.segmentPipelines == nil {
		m.segmentPipelines = map[string]*segmentPipeline{}
	}
	m.segmentPipelines[mt.ID] = &segmentPipeline{}
	m.flashInfo(tr("Recording… press r to stop"))
	cmds := []tea.Cmd{clearErrAfter(), recordSegmentTick()}
	// The status line's elapsed time (recordingIndicator) only redraws when
	// something triggers a render; without this it sits still and jumps
	// several seconds at once whenever something else happens to redraw —
	// see needsSecondTick.
	if !m.timerTickOn {
		m.timerTickOn = true
		cmds = append(cmds, timerTick())
	}
	return m, tea.Batch(cmds...)
}

// promptInstallFFmpeg is reached when "r" finds ffmpeg missing. tjek can run
// the platform's package manager itself (ffmpegInstallCommand), but that's a
// system-modifying action, so it only ever runs after this explicit y/n —
// never silently, and only when a command to run was actually found; with
// none available this falls back to the same copy-paste hint as before.
func (m model) promptInstallFFmpeg(meetingID string) (tea.Model, tea.Cmd) {
	name, args, ok := ffmpegInstallCommand()
	if !ok {
		m.flashError(fmt.Sprintf(tr("ffmpeg not found — %s"), ffmpegInstallHint()))
		return m, clearErrAfter()
	}
	m.pendingFFmpegInstallMeetingID = meetingID
	m.mode = modeConfirm
	m.confirmMsg = fmt.Sprintf(tr("ffmpeg isn't installed. Run '%s' now? (y/n)"),
		strings.Join(append([]string{name}, args...), " "))
	m.confirmOnYes = (*model).confirmInstallFFmpeg
	return m, nil
}

// confirmInstallFFmpeg is modeConfirm's y/n action for promptInstallFFmpeg —
// it re-resolves the command rather than threading it through confirmMsg, so
// what runs is always freshly checked against PATH, not a stale string.
func (m *model) confirmInstallFFmpeg() tea.Cmd {
	meetingID := m.pendingFFmpegInstallMeetingID
	m.pendingFFmpegInstallMeetingID = ""
	name, args, ok := ffmpegInstallCommand()
	if !ok {
		m.flashError(fmt.Sprintf(tr("ffmpeg not found — %s"), ffmpegInstallHint()))
		return clearErrAfter()
	}
	return ffmpegInstallCmd(meetingID, name, args)
}

// handleFFmpegInstallFinished resumes the recording that prompted the
// install, once ffmpeg is confirmed to actually be on PATH — the install
// command can exit 0 without installing anything a human would call
// success (a cancelled sudo prompt, a declined agreement), so this checks
// for ffmpeg itself rather than trusting the exit code alone.
func (m model) handleFFmpegInstallFinished(msg ffmpegInstallFinishedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.flashError(fmt.Sprintf(tr("ffmpeg install failed: %v"), msg.err))
		return m, clearErrAfter()
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		m.flashError(tr("ffmpeg still isn't on PATH after install — see the output above"))
		return m, clearErrAfter()
	}
	mt := m.meetingByID(msg.meetingID)
	if mt == nil || !mt.CanRecord() {
		m.flashSuccess(tr("ffmpeg installed"))
		return m, clearErrAfter()
	}
	return m.startRecordingFor(mt)
}

// handleSegmentClosed reacts to one recorded chunk's ffmpeg process
// finishing — either because recordSegmentDuration elapsed and the next
// chunk needs to start (final=false), or because "r" asked to stop the
// whole recording (final=true). The next chunk is started first, so the
// gap in the recording is only as long as closing the old process and
// launching the new one takes, before this hands the chunk that just
// closed off to be transcribed.
func (m model) handleSegmentClosed(msg segmentClosedMsg) (tea.Model, tea.Cmd) {
	mt := m.meetingByID(msg.meetingID)
	if mt == nil {
		return m, nil
	}
	if msg.err != nil {
		mt.Status = meeting.StatusError
		mt.ErrorMsg = msg.err.Error()
		_ = saveMeeting(mt)
		if m.recordingMeetingID == msg.meetingID {
			m.recorder = nil
			m.recordingMeetingID = ""
		}
		delete(m.segmentPipelines, msg.meetingID)
		m.flashError(fmt.Sprintf(tr("Recording error: %v"), msg.err))
		return m, clearErrAfter()
	}

	var cmds []tea.Cmd
	if !msg.final {
		nextIndex := msg.index + 1
		rec, err := startMeetingSegment(mt, nextIndex, m.ffmpegInput)
		if err != nil {
			mt.Status = meeting.StatusError
			mt.ErrorMsg = err.Error()
			_ = saveMeeting(mt)
			m.recorder = nil
			m.recordingMeetingID = ""
			delete(m.segmentPipelines, msg.meetingID)
			m.flashError(fmt.Sprintf(tr("Could not continue recording: %v"), err))
			return m, clearErrAfter()
		}
		m.recorder = rec
		m.recordSegmentIndex = nextIndex
		cmds = append(cmds, recordSegmentTick())
	}

	if m.aiKeys.OpenAI == "" {
		// Nothing to transcribe with — handleSegmentTranscribed still does
		// the ordering/rollover bookkeeping and lands the meeting on
		// StatusDraft once the last chunk is in, the same as it would
		// after a real transcription call, just without one.
		next, cmd := m.handleSegmentTranscribed(segmentTranscribedMsg{meetingID: msg.meetingID, index: msg.index, final: msg.final})
		m = next.(model)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	}
	cmds = append(cmds, transcribeSegmentCmd(msg.meetingID, msg.path, msg.index, msg.final, m.aiKeys))
	return m, tea.Batch(cmds...)
}

// handleSegmentTranscribed records one chunk's transcribed text — or a
// placeholder if transcribing it failed, since a dropped network call
// shouldn't cost the rest of a long meeting — and flushes every chunk
// that's now ready in order, so chunks finishing out of turn still produce
// an in-order transcript instead of racing each other onto the end of it.
// Once the final chunk flushes, the meeting moves on exactly the way it
// would have after one single-shot transcription: StatusDraft with no
// OpenAI key configured, otherwise straight into the AI pass.
func (m model) handleSegmentTranscribed(msg segmentTranscribedMsg) (tea.Model, tea.Cmd) {
	mt := m.meetingByID(msg.meetingID)
	if mt == nil {
		return m, nil
	}

	text := msg.text
	if msg.err != nil {
		text = tr("[transcription failed for this part]")
		m.flashError(fmt.Sprintf(tr("A chunk failed to transcribe: %v"), msg.err))
	}

	if m.segmentPipelines == nil {
		m.segmentPipelines = map[string]*segmentPipeline{}
	}
	pipeline := m.segmentPipelines[msg.meetingID]
	if pipeline == nil {
		pipeline = &segmentPipeline{}
		m.segmentPipelines[msg.meetingID] = pipeline
	}
	if pipeline.results == nil {
		pipeline.results = map[int]segmentResult{}
	}
	pipeline.results[msg.index] = segmentResult{text: text, final: msg.final}

	finished := false
	for {
		res, ok := pipeline.results[pipeline.appendCursor]
		if !ok {
			break
		}
		if trimmed := strings.TrimSpace(res.text); trimmed != "" {
			if mt.Transcript == "" {
				mt.Transcript = trimmed
			} else {
				mt.Transcript = mt.Transcript + " " + trimmed
			}
		}
		delete(pipeline.results, pipeline.appendCursor)
		pipeline.appendCursor++
		if res.final {
			finished = true
			break
		}
	}

	if err := saveMeeting(mt); err != nil {
		m.flashError(fmt.Sprintf(tr("Error saving meeting: %v"), err))
	}
	if !finished {
		return m, clearErrAfter()
	}
	delete(m.segmentPipelines, msg.meetingID)

	if m.aiKeys.OpenAI == "" {
		mt.Status = meeting.StatusDraft
		_ = saveMeeting(mt)
		m.flashInfo(tr("Recording saved. Add an OpenAI API key in Settings to transcribe it, or press n to type notes."))
		return m, clearErrAfter()
	}
	mt.Status = meeting.StatusSummarizing
	mt.ErrorMsg = ""
	if err := saveMeeting(mt); err != nil {
		m.flashError(fmt.Sprintf(tr("Error saving meeting: %v"), err))
	}
	return m, tea.Batch(clearErrAfter(),
		runAIPassCmd(mt.ID, mt.Input(), m.aiProvider, m.aiKeys, m.cache.projectNames, m.getAllTagsSorted()))
}

// ── Generate (summarize + extract) ──────────────────────────────────────────

func (m model) handleMeetingsRunAI() (tea.Model, tea.Cmd) {
	mt := m.meetingForEditorTarget()
	if mt == nil || !mt.CanRunAI() {
		return m, nil
	}
	mt.Status = meeting.StatusSummarizing
	mt.ErrorMsg = ""
	if err := saveMeeting(mt); err != nil {
		m.flashError(fmt.Sprintf(tr("Error saving meeting: %v"), err))
	}
	m.flashInfo(tr("Summarizing and looking for action items…"))
	return m, tea.Batch(clearErrAfter(),
		runAIPassCmd(mt.ID, mt.Input(), m.aiProvider, m.aiKeys, m.cache.projectNames, m.getAllTagsSorted()))
}

func (m model) handleAIPassDone(msg aiPassDoneMsg) (tea.Model, tea.Cmd) {
	mt := m.meetingByID(msg.meetingID)
	if mt == nil {
		return m, nil
	}
	if msg.err != nil {
		mt.Status = meeting.StatusError
		mt.ErrorMsg = msg.err.Error()
		_ = saveMeeting(mt)
		m.flashError(fmt.Sprintf(tr("AI pass failed: %v"), msg.err))
		return m, clearErrAfter()
	}
	mt.Summary = msg.summary
	mt.Status = meeting.StatusReady
	if err := saveMeeting(mt); err != nil {
		m.flashError(fmt.Sprintf(tr("Error saving meeting: %v"), err))
	}
	suggestions := make([]meeting.Suggestion, 0, len(msg.suggestions))
	for _, s := range msg.suggestions {
		sg := meeting.NewSuggestion(mt.ID, s.Title)
		sg.SuggestedProject = s.Project
		sg.SuggestedTags = s.Tags
		sg.SuggestedPriority = s.Priority
		sg.SuggestedDue = s.Due
		suggestions = append(suggestions, sg)
	}
	if err := replaceSuggestions(mt.ID, suggestions); err != nil {
		m.flashError(fmt.Sprintf(tr("Error saving action items: %v"), err))
	}
	if m.openMeetingID == mt.ID {
		m.meetingSuggestions = suggestions
		m.meetingReviewCursor = -1
		if len(suggestions) > 0 {
			m.meetingReviewCursor = 0
		}
	}
	m.flashSuccess(fmt.Sprintf(tr("Found %d action item(s) to review"), len(suggestions)))
	return m, clearErrAfter()
}

// ── Review: accept / reject / edit ──────────────────────────────────────────

func (m model) acceptFocusedSuggestion() (tea.Model, tea.Cmd) {
	if m.meetingReviewCursor < 0 || m.meetingReviewCursor >= len(m.meetingSuggestions) {
		return m, nil
	}
	s := m.meetingSuggestions[m.meetingReviewCursor]
	t := acceptSuggestion(s)
	m.pushUndo(tr("accept action item"), t.ID)
	m.add(t)
	m.markModified(t.ID)
	saveLastAddedID(t.ID)

	s.Resolved, s.Accepted, s.CreatedTaskID = true, true, t.ID
	m.meetingSuggestions[m.meetingReviewCursor] = s
	if err := resolveSuggestion(s.ID, true, t.ID); err != nil {
		m.flashError(fmt.Sprintf(tr("Error saving review: %v"), err))
	}
	m.flashSuccess(fmt.Sprintf(tr("Added task: %s"), truncate(t.Title, 40)))
	m.advanceReviewCursorOrFinish()
	return m, clearErrAfter()
}

func (m model) rejectFocusedSuggestion() (tea.Model, tea.Cmd) {
	if m.meetingReviewCursor < 0 || m.meetingReviewCursor >= len(m.meetingSuggestions) {
		return m, nil
	}
	s := m.meetingSuggestions[m.meetingReviewCursor]
	s.Resolved, s.Accepted = true, false
	m.meetingSuggestions[m.meetingReviewCursor] = s
	if err := resolveSuggestion(s.ID, false, ""); err != nil {
		m.flashError(fmt.Sprintf(tr("Error saving review: %v"), err))
	}
	m.advanceReviewCursorOrFinish()
	return m, nil
}

// advanceReviewCursorOrFinish moves the review cursor to the next unresolved
// suggestion, or — once every suggestion has been accepted or rejected —
// marks the meeting Reviewed and parks the cursor.
func (m *model) advanceReviewCursorOrFinish() {
	n := len(m.meetingSuggestions)
	for i := 1; i <= n; i++ {
		idx := (m.meetingReviewCursor + i) % n
		if !m.meetingSuggestions[idx].Resolved {
			m.meetingReviewCursor = idx
			return
		}
	}
	m.meetingReviewCursor = -1
	if mt := m.meetingByID(m.openMeetingID); mt != nil && mt.Status != meeting.StatusReviewed {
		mt.Status = meeting.StatusReviewed
		if err := saveMeeting(mt); err != nil {
			m.flashError(fmt.Sprintf(tr("Error saving meeting: %v"), err))
		}
	}
}

// handleMeetingsDeleteOrReject is "x" on the Meetings tab: reject the focused
// suggestion while reviewing one, otherwise delete the meeting under the
// cursor (with the same confirm prompt every other delete uses).
func (m model) handleMeetingsDeleteOrReject() (tea.Model, tea.Cmd) {
	if m.pane == paneDetail && m.meetingReviewCursor >= 0 && m.meetingReviewCursor < len(m.meetingSuggestions) {
		return m.rejectFocusedSuggestion()
	}
	mt := m.currentMeeting()
	if mt == nil {
		return m, nil
	}
	m.mode = modeConfirm
	m.pendingDeleteID = mt.ID
	m.confirmOnYes = (*model).confirmDeleteMeeting
	m.confirmMsg = fmt.Sprintf(tr("Delete meeting '%s'? (y/n)"), truncate(mt.Title, 40))
	return m, nil
}

func (m *model) confirmDeleteMeeting() tea.Cmd {
	id := m.pendingDeleteID
	if err := deleteMeetingSoft(id); err != nil {
		m.flashError(fmt.Sprintf(tr("Error deleting meeting: %v"), err))
		return clearErrAfter()
	}
	for i, mt := range m.meetings {
		if mt.ID == id {
			m.meetings = append(m.meetings[:i], m.meetings[i+1:]...)
			break
		}
	}
	if m.cursor >= len(m.meetings) && m.cursor > 0 {
		m.cursor--
	}
	delete(m.segmentPipelines, id)
	return nil
}

// suggestionQuickAddSeed renders s as a quick-add line so updateEditSuggestion
// can reuse parseQuickAdd instead of a bespoke per-field editor — the same
// syntax the user already knows from adding a task. A project name with a
// space is left out of the seed: quick-add's whitespace tokenisation can't
// round-trip it (see quickAddSeed), so re-typing it would silently drop it.
func suggestionQuickAddSeed(s meeting.Suggestion) string {
	parts := []string{s.Title}
	if s.SuggestedProject != "" && !strings.ContainsAny(s.SuggestedProject, " \t") {
		parts = append(parts, "@"+s.SuggestedProject)
	}
	for _, tag := range s.SuggestedTags {
		parts = append(parts, "#"+tag)
	}
	if s.SuggestedPriority != "" {
		parts = append(parts, "p:"+s.SuggestedPriority)
	}
	if s.SuggestedDue != "" {
		parts = append(parts, "due:"+s.SuggestedDue)
	}
	return strings.Join(parts, " ")
}

// updateEditSuggestion handles "e" on a focused review row: a single
// quick-add-syntax line, pre-filled, committed through parseQuickAdd.
func (m model) updateEditSuggestion(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			if m.meetingReviewCursor >= 0 && m.meetingReviewCursor < len(m.meetingSuggestions) {
				parsed := parseQuickAdd(m.textInput.Value())
				s := m.meetingSuggestions[m.meetingReviewCursor]
				if parsed.title != "" {
					s.Title = parsed.title
				}
				s.SuggestedProject = parsed.project
				s.SuggestedTags = parsed.tags
				if parsed.hasPriority {
					s.SuggestedPriority = strings.ToLower(priorityLetter(parsed.priority))
				}
				if !parsed.dueDate.IsZero() {
					s.SuggestedDue = parsed.dueDate.Format("2006-01-02")
				}
				m.meetingSuggestions[m.meetingReviewCursor] = s
				if err := updateSuggestionEdits(s); err != nil {
					m.flashError(fmt.Sprintf(tr("Error saving edit: %v"), err))
				}
			}
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

// startEditMeetingText is "n" (notes) or "T" (transcript) on the Meetings
// tab: it seeds meetingTextarea from the target meeting's current field,
// focuses it, and switches to modeEditMeetingText — an in-app multi-line
// editor (both ctrl+s and esc save) rather than a round trip through
// $EDITOR, which felt like a context switch for what's often a one-line note.
func (m *model) startEditMeetingText(field string) tea.Cmd {
	mt := m.meetingForEditorTarget()
	if mt == nil {
		return nil
	}
	current := mt.Notes
	if field == "transcript" {
		current = mt.Transcript
	}
	m.meetingEditID = mt.ID
	m.meetingEditField = field
	m.meetingTextarea.SetValue(current)
	m.meetingTextarea.CursorEnd()
	m.mode = modeEditMeetingText
	return m.meetingTextarea.Focus()
}

// updateEditMeetingText drives modeEditMeetingText: both ctrl+s and esc
// commit the textarea's value into the target field and leave edit mode —
// esc does not discard, so pressing it by reflex (or to reach for another
// key) can never lose what was just typed. Anything else is ordinary
// textarea editing.
func (m model) updateEditMeetingText(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "ctrl+s", "esc":
			return m.saveMeetingTextAndExit()
		}
	}
	var cmd tea.Cmd
	m.meetingTextarea, cmd = m.meetingTextarea.Update(msg)
	return m, cmd
}

// saveMeetingTextAndExit is the shared body of ctrl+s and esc in
// modeEditMeetingText: write the textarea's current value into whichever
// field (Notes or Transcript) is being edited and return to modeNormal.
func (m model) saveMeetingTextAndExit() (tea.Model, tea.Cmd) {
	id, field := m.meetingEditID, m.meetingEditField
	m.meetingEditID = ""
	m.meetingEditField = ""
	m.meetingTextarea.Blur()
	m.mode = modeNormal
	mt := m.meetingByID(id)
	if mt == nil {
		return m, nil
	}
	edited := m.meetingTextarea.Value()
	changed := false
	switch field {
	case "transcript":
		changed = edited != mt.Transcript
		mt.Transcript = edited
	default: // "notes", and the fallback for a stale/empty field value
		changed = edited != mt.Notes
		mt.Notes = edited
	}
	if !changed {
		return m, nil
	}
	if err := saveMeeting(mt); err != nil {
		m.flashError(fmt.Sprintf(tr("Error saving meeting: %v"), err))
		return m, clearErrAfter()
	}
	m.flashSuccess(tr("Saved"))
	return m, clearErrAfter()
}

// startEditFocusedSuggestion opens modeEditSuggestion seeded from the
// focused review row — bound to "e" alongside the Meetings tab's other keys;
// see view_meetings.go for the hint text.
func (m *model) startEditFocusedSuggestion() tea.Cmd {
	if m.meetingReviewCursor < 0 || m.meetingReviewCursor >= len(m.meetingSuggestions) {
		return nil
	}
	seed := suggestionQuickAddSeed(m.meetingSuggestions[m.meetingReviewCursor])
	m.mode = modeEditSuggestion
	m.textInput.SetValue(seed)
	m.textInput.SetCursor(len([]rune(seed)))
	m.textInput.Placeholder = tr("Edit action item (quick-add syntax)...")
	m.textInput.Focus()
	return textinput.Blink
}
