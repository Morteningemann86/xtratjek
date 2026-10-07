package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/Iliorn/tjek/rank"
	"github.com/Iliorn/tjek/todo"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// ── Top-level Update ──────────────────────────────────────────────────────────

// Update is the Bubble Tea entry point. It delegates the real work to
// dispatch, then layers on a single concern: if the user just left a modal
// mode and a watcher reload was deferred while they were typing, schedule
// the reload now so the deferred external change isn't silently lost.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// A panic here is a bug, but it is also the user's data: the guard writes
	// a report from the panic site and flushes what the save debounce still
	// owes, then re-panics so Bubble Tea restores the terminal (crash.go).
	defer m.crashGuard("update", msg)
	if traceCh != nil {
		t0 := time.Now()
		defer func() { lastUpdate, lastUpdateKind = time.Since(t0), msgKind(msg) }()
	}
	next, cmd := m.dispatch(msg)
	n, ok := next.(model)
	if !ok {
		return next, cmd
	}
	if n.mode == modeNormal && n.watcher != nil && n.watcher.drainPending() {
		repo := n.repo
		reload := func() tea.Msg {
			todos, err := repo.Load()
			return reloadedMsg{todos: todos, err: err}
		}
		if cmd == nil {
			cmd = reload
		} else {
			cmd = tea.Batch(cmd, reload)
		}
	}
	return n, cmd
}

func (m model) dispatch(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.frameTime = time.Now()

	// ctrl+c quits from anywhere — every mode, both panes — after flushing any
	// mutation still inside the 300ms save debounce. Bubble Tea delivers it as
	// an ordinary key (there is no built-in quit), so it is handled here
	// rather than per mode.
	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "ctrl+c" {
		m.flushPendingWrites()
		m.closeWatcher()
		return m, tea.Quit
	}

	if sz, ok := msg.(tea.WindowSizeMsg); ok {
		m.termWidth = sz.Width
		m.termHeight = sz.Height
		m.invalidateDetailCache()
	}

	// A user-rebound key is translated into the key the handlers case on, so
	// the dispatch below never has to know about settings.json "keys". Only in
	// normal mode: a modal owns its keys (y/n on a confirm, the text being
	// typed), and rewriting those would corrupt input.
	if key, ok := msg.(tea.KeyMsg); ok && m.mode == modeNormal {
		resolved, deliver := resolveKeyOverride(m.currentKeyCtx(), key.String())
		if !deliver {
			return m, nil
		}
		// j/k after the override pass, so a rebind onto either of them wins
		// over the vim alias rather than being shadowed by it.
		resolved = navAlias(resolved)
		if resolved != key.String() {
			msg = keyMsgFor(resolved)
		}
	}

	switch msg := msg.(type) {
	case clearErrMsg:
		m.err = ""
		m.errKind = toastError
		return m, nil
	case timerTickMsg:
		if m.anyTimerRunning() {
			// Heartbeat the running timer's last_seen at most once a minute so
			// the stale-timer recoverer never mistakes this live timer for an
			// abandoned one. recordSelfSave keeps the fs watcher from reloading
			// on our own write. The write itself runs as a tea.Cmd — off the
			// Update goroutine — so a busy DB (concurrent sync/CLI write inside
			// busy_timeout) can't freeze the UI for up to 5s.
			if time.Since(m.lastTimerHeartbeat) >= time.Minute {
				m.lastTimerHeartbeat = time.Now()
				// Keep the in-memory entries in step with the DB heartbeat —
				// see stampRunningTimersSeen for why saves depend on this.
				m.stampRunningTimersSeen(m.lastTimerHeartbeat)
				if m.watcher != nil {
					m.watcher.recordSelfSave()
				}
				return m, tea.Batch(timerTick(), func() tea.Msg {
					_ = heartbeatRunningTimers(db, time.Now())
					return nil
				})
			}
			return m, timerTick()
		}
		if m.recorder != nil {
			return m, timerTick()
		}
		m.timerTickOn = false
		return m, nil
	case updateDoneMsg:
		if msg.err != nil {
			m.flashError(fmt.Sprintf("Update failed: %v", msg.err))
			m.updateStatus = tr("Update failed")
		} else {
			m.flashSuccess(tr("Updated! Restart tjek to apply."))
			m.updateStatus = tr("Updated; restart to apply")
		}
		return m, clearErrAfter()
	case updateCheckMsg:
		if msg.err != nil {
			m.flashError(fmt.Sprintf("Update check failed: %v", msg.err))
			m.updateStatus = tr("Check failed")
			return m, clearErrAfter()
		}
		// planUpdate (version.go) owns the verdict, shared with `tjek update`
		// so the two surfaces cannot disagree about the same binary; the
		// sentences stay here because they are translated and the CLI's aren't.
		switch action, hint := planUpdate(appVersion, msg.latest); action {
		case updateUpToDate:
			m.updateStatus = tr("Up to date (") + appVersion + ")"
			return m, nil
		case updateLocalBuild:
			m.updateStatus = tr("Latest release: ") + msg.latest + tr("; this is a local build (") + appVersion + ")"
			m.flashInfo(m.updateStatus)
			return m, clearErrAfter()
		case updateManaged:
			m.updateStatus = fmt.Sprintf(tr("Update available: %s. Run `%s`"), msg.latest, hint)
			m.flashInfo(m.updateStatus)
			return m, clearErrAfter()
		}
		// Newer release available — ask before pulling it.
		m.updateStatus = tr("Update available: ") + msg.latest
		m.mode = modeConfirmUpdate
		m.confirmMsg = msg.latest + tr(" is available. Update now? (y/n)")
		return m, nil
	case saveDoneMsg:
		return m, nil
	case syncTickMsg:
		cmds := []tea.Cmd{syncTick()}
		if m.autoSync {
			cmds = append(cmds, m.backgroundSync())
			// Mid-session enable: start the real-time listener if sync was just
			// turned on (it isn't running yet) and arm its reader once.
			if m.liveSync == nil {
				if ls := startLiveSync(m.syncCfg); ls != nil {
					m.liveSync = ls
					cmds = append(cmds, waitForSyncEvent(ls.C))
				}
			}
		}
		if p := m.probeServer(); p != nil {
			cmds = append(cmds, p)
		}
		return m, tea.Batch(cmds...)
	case syncEventMsg:
		// Server signalled a change. Re-arm the listener and pull now.
		var cmds []tea.Cmd
		if m.liveSync != nil {
			cmds = append(cmds, waitForSyncEvent(m.liveSync.C))
		}
		if m.autoSync {
			cmds = append(cmds, m.backgroundSync())
		}
		if len(cmds) == 0 {
			return m, nil
		}
		return m, tea.Batch(cmds...)
	case syncDoneMsg:
		return m.handleSyncDone(msg)
	case reminderTickMsg:
		cmds := []tea.Cmd{reminderTick()}
		if !startOfDay(m.cache.builtAt).Equal(startOfDay(msg.at)) {
			m.markCacheDirty()
		}
		send, flashed := m.checkReminder(msg.at)
		if send != nil {
			cmds = append(cmds, send)
		}
		if flashed {
			cmds = append(cmds, clearErrAfter())
		}
		return m, tea.Batch(cmds...)
	case exportTickMsg:
		return m, m.exportTick()
	case exportDoneMsg:
		if msg.err != nil {
			m.exportDirty = true // retried with the next change
			m.flashError(fmt.Sprintf(tr("Auto-export failed: %v"), msg.err))
			return m, clearErrAfter()
		}
		return m, nil
	case importDoneMsg:
		return m.handleImportDone(msg)
	case reminderSentMsg:
		if msg.err != nil {
			// The reminder is the news, not the pop-up: keep it on screen and
			// say only that the desktop could not show it. The reason is
			// `tjek remind --now`'s to print, where there is room for it.
			m.flashInfo(msg.title + " · " + tr("desktop pop-up unavailable"))
			return m, clearErrAfter()
		}
		return m, nil
	case serverProbeMsg:
		// Only flag "external" when we aren't the one serving in-process.
		m.serverExternal = msg.reachable && m.inprocServer == nil
		return m, nil
	case saveErrMsg:
		m.flashError(fmt.Sprintf("Error saving tasks: %v", msg.err))
		return m, clearErrAfter()
	case editorFinishedMsg:
		return m.handleEditorFinished(msg)
	case chatReplyMsg:
		return m.handleChatReply(msg)
	case whisperModelDownloadDoneMsg:
		return m.handleWhisperModelDownloadDone(msg)
	case whisperCLIInstallFinishedMsg:
		return m.handleWhisperCLIInstallFinished(msg)
	case segmentClosedMsg:
		return m.handleSegmentClosed(msg)
	case segmentTranscribedMsg:
		return m.handleSegmentTranscribed(msg)
	case recordSegmentTickMsg:
		// A stale tick from a recording that has already fully stopped
		// (m.recorder nil) is simply dropped — handleMeetingsToggleRecord's
		// final close doesn't try to cancel the pending tea.Tick, since
		// there's no handle to cancel it with; this guard is what makes
		// that safe instead of acting on a recorder that's gone.
		if m.recorder == nil {
			return m, nil
		}
		rec, id, index := m.recorder, m.recordingMeetingID, m.recordSegmentIndex
		path, err := segmentRecordingPath(id, index)
		if err != nil {
			m.flashError(fmt.Sprintf(tr("Error rolling over recording: %v"), err))
			return m, clearErrAfter()
		}
		return m, closeSegmentCmd(rec, id, path, index, false)
	case ffmpegInstallFinishedMsg:
		return m.handleFFmpegInstallFinished(msg)
	case aiPassDoneMsg:
		return m.handleAIPassDone(msg)
	case suggestionsLoadedMsg:
		if msg.err == nil && msg.meetingID == m.openMeetingID {
			m.meetingSuggestions = msg.suggestions
			if m.meetingReviewCursor < 0 && len(m.meetingSuggestions) > 0 {
				m.meetingReviewCursor = 0
			}
		}
		return m, nil
	case saveTickMsg:
		m.saveScheduled = false
		if m.savePending {
			m.savePending = false
			// Drain only the dirty IDs and tombstones from the Store. The
			// per-task deep copies drainDirty makes are what keep this save
			// goroutine safe from the mutations the Update goroutine keeps
			// making while it runs.
			dirty, tombstones := m.Store.drainDirty()
			if len(dirty) == 0 && len(tombstones) == 0 {
				return m, nil
			}
			repo := m.repo
			if m.watcher != nil {
				// Record the timestamp BEFORE the save so a fast fs event
				// firing during the write is still inside the suppression
				// window. The save goroutine doesn't need to update this.
				m.watcher.recordSelfSave()
			}
			return m, tea.Batch(func() tea.Msg {
				if err := repo.Save(dirty, tombstones); err != nil {
					return saveErrMsg{err}
				}
				return saveDoneMsg{}
			}, m.exportSoon())
		}
		return m, nil
	case dbChangedMsg:
		// External writer (CLI, another process) touched the DB. Decide
		// whether to reload now or defer until the user exits a modal mode.
		// Always re-arm the watcher channel listener.
		var cmds []tea.Cmd
		if m.watcher != nil {
			cmds = append(cmds, waitForDBChange(m.watcher.ch))
			if m.watcher.shouldReloadNow(time.Now(), m.mode) {
				repo := m.repo
				cmds = append(cmds, func() tea.Msg {
					todos, err := repo.Load()
					return reloadedMsg{todos: todos, err: err}
				})
			}
		}
		if len(cmds) == 0 {
			return m, nil
		}
		return m, tea.Batch(cmds...)
	case reloadedMsg:
		if msg.err != nil {
			m.flashError(fmt.Sprintf("External reload failed: %v", msg.err))
			return m, clearErrAfter()
		}
		// Atomic swap: rebuild the Store from the freshly-loaded task set,
		// invalidate caches, and follow the same task ID across the new
		// ordering so the cursor stays anchored where the user expected.
		//
		// The swap must not wipe what only exists in memory: the undo stack,
		// and any mutation still inside the save debounce (dirty tasks and
		// pending tombstones the snapshot predates). Those local changes are
		// newer than anything on disk — overlay them on the loaded set and
		// carry the change set across so the scheduled save still flushes it.
		// A reload the user cannot see is the common case, not the exception:
		// the watcher fires on our own WAL writes, on a sync that merged
		// nothing, on a checkpoint. Rebuilding the whole Store for those costs
		// ~15ms at a couple of thousand tasks — on the Update goroutine, so it
		// lands as a stutter on whatever key is pressed next. Compare a cheap
		// fingerprint first and skip the swap when the snapshot says what we
		// already have. Pending local changes make the snapshot stale by
		// definition, so the guard only applies when there are none.
		if len(m.dirtyIDs) == 0 && len(m.tombstones) == 0 && m.sameAsLoaded(msg.todos) {
			return m, nil
		}

		taskID := m.currentTaskID()
		undo := m.undoStack
		dirtyIDs := m.dirtyIDs
		tombstones := m.tombstones
		dirtyTasks := make(map[string]todo.Todo, len(dirtyIDs))
		for id := range dirtyIDs {
			if t := m.get(id); t != nil {
				dirtyTasks[id] = copyTodo(*t)
			}
		}
		m.Store = Store{}
		m.Store.ensureTasks()
		m.undoStack = undo
		m.dirtyIDs = dirtyIDs
		m.tombstones = tombstones
		for i := range msg.todos {
			t := msg.todos[i]
			if _, dead := tombstones[t.ID]; dead {
				continue // deleted locally, deletion not yet flushed — stays dead
			}
			if d, ok := dirtyTasks[t.ID]; ok {
				t = d // unsaved local edit is newer than the DB snapshot
			}
			m.Store.add(t)
		}
		// Dirty tasks the snapshot doesn't know yet (created locally, unflushed).
		for id, d := range dirtyTasks {
			if m.get(id) == nil {
				m.Store.add(d)
			}
		}
		m.markCacheDirty()
		m.refreshCaches()
		m.followTask(taskID)
		return m, m.exportSoon() // another process changed the store
	}

	// All handler paths feed through the common tail below so the dirty
	// flag set by a modal mutation (add task, confirm delete, edit title,
	// etc.) schedules the 300ms save immediately — not on the next
	// keystroke, or quitting right after the modal Enter would lose it.
	var newModel tea.Model
	var cmd tea.Cmd
	switch m.mode {
	case modeHelp:
		newModel, cmd = m.updateHelp(msg)
	case modeExplain:
		newModel, cmd = m.updateExplain(msg)
	case modeBoardCarry:
		newModel, cmd = m.updateBoardCarry(msg)
	case modeBoardCard:
		newModel, cmd = m.updateBoardCard(msg)
	case modeConfirm:
		newModel, cmd = m.updateConfirm(msg)
	case modeConfirmUpdate:
		newModel, cmd = m.updateConfirmUpdate(msg)
	case modeEditTimeEntry:
		newModel, cmd = m.updateEditTimeEntry(msg)
	case modeIdlePrompt:
		newModel, cmd = m.updateIdlePrompt(msg)
	case modeInput:
		newModel, cmd = m.updateInput(msg)
	case modeEditComment:
		newModel, cmd = m.updateEditComment(msg)
	case modeEditTag:
		newModel, cmd = m.updateEditTag(msg)
	case modeEditTitle:
		newModel, cmd = m.updateEditTitle(msg)
	case modeEditDue:
		newModel, cmd = m.updateEditDue(msg)
	case modeEditProjectInline:
		newModel, cmd = m.updateEditProjectInline(msg)
	case modePalette:
		newModel, cmd = m.updatePalette(msg)
	case modeEditStages:
		newModel, cmd = m.updateEditStages(msg)
	case modeEditExportFolder:
		newModel, cmd = m.updateEditExportFolder(msg)
	case modeImportFile:
		newModel, cmd = m.updateImportFile(msg)
	case modeEditSyncURL:
		newModel, cmd = m.updateEditSyncURL(msg)
	case modeEditSyncToken:
		newModel, cmd = m.updateEditSyncToken(msg)
	case modeEditServerListen:
		newModel, cmd = m.updateEditServerListen(msg)
	case modeEditServerToken:
		newModel, cmd = m.updateEditServerToken(msg)
	case modeEditAnthropicKey:
		newModel, cmd = m.updateEditAnthropicKey(msg)
	case modeEditOpenAIKey:
		newModel, cmd = m.updateEditOpenAIKey(msg)
	case modeEditGeminiKey:
		newModel, cmd = m.updateEditGeminiKey(msg)
	case modeEditMistralKey:
		newModel, cmd = m.updateEditMistralKey(msg)
	case modeEditFFmpegInput:
		newModel, cmd = m.updateEditFFmpegInput(msg)
	case modeEditWhisperBinOverride:
		newModel, cmd = m.updateEditWhisperBinOverride(msg)
	case modeEditWhisperLanguage:
		newModel, cmd = m.updateEditWhisperLanguage(msg)
	case modeAddMeeting:
		newModel, cmd = m.updateAddMeeting(msg)
	case modeEditSuggestion:
		newModel, cmd = m.updateEditSuggestion(msg)
	case modeEditMeetingText:
		newModel, cmd = m.updateEditMeetingText(msg)
	case modeChatInput:
		newModel, cmd = m.updateChatInput(msg)
	case modeAddSubtask:
		newModel, cmd = m.updateAddSubtask(msg)
	case modeEditSubtask:
		newModel, cmd = m.updateEditSubtask(msg)
	case modeAddTimeEntry:
		newModel, cmd = m.updateAddTimeEntry(msg)
	case modeSearch:
		newModel, cmd = m.updateSearch(msg)
	case modeSearchDep:
		newModel, cmd = m.updateSearchDep(msg)
	case modeSearchTag:
		newModel, cmd = m.updateSearchTag(msg)
	case modeSearchProject:
		newModel, cmd = m.updateSearchProject(msg)
	case modeSearchTagTab:
		newModel, cmd = m.updateSearchTagTab(msg)
	default:
		switch {
		case m.tab == tabMeetings && m.pane == paneDetail:
			// Meetings' detail pane (recording/generating/reviewing) has no
			// equivalent in updateDetail, which is Tasks-detail-specific
			// (m.currentTodo(), m.detail field cursor) — its own self-
			// contained handler, the same way updateDetail itself is.
			newModel, cmd = m.updateMeetingsDetail(msg)
		case m.pane == paneList:
			newModel, cmd = m.updateList(msg)
		default:
			newModel, cmd = m.updateDetail(msg)
		}
	}

	if nm, ok := newModel.(model); ok {
		nm.clampCursors()
		if nm.dirty {
			nm.dirty = false
			nm.savePending = true
			if !nm.saveScheduled {
				nm.saveScheduled = true
				saveCmd := scheduleSave()
				if cmd != nil {
					return nm, tea.Batch(cmd, saveCmd)
				}
				return nm, saveCmd
			}
			return nm, cmd
		}
		return nm, cmd
	}
	return newModel, cmd
}

// ── Editor handling ───────────────────────────────────────────────────────────

func (m *model) openEditorForNotes() tea.Cmd {
	t := m.currentTodo()
	if t == nil {
		return nil
	}
	taskID := t.ID

	if err := writeNotesFile(taskID, t.Notes); err != nil {
		m.flashError(fmt.Sprintf("Error writing description file: %v", err))
		return clearErrAfter()
	}

	editorCmd := resolveEditorCmd()
	if editorCmd == "" {
		if runtime.GOOS == "windows" {
			m.flashError(tr("No editor found. Set EDITOR permanently, e.g: setx EDITOR notepad (then restart tjek)"))
		} else {
			m.flashError(tr("No editor found. Set $EDITOR permanently, e.g: echo 'set -Ux EDITOR /usr/lib/helix/hx' >> ~/.config/fish/config.fish"))
		}
		return clearErrAfter()
	}

	m.editorTaskID = taskID
	m.editorToInput = false
	return execEditor(editorCmd, taskID, false)
}

// openEditorForInput backs the ctrl+e escape hatch from the single-line
// comment inputs: it seeds $EDITOR with the current draft and, on
// return, reloads the edited text into the input (handleEditorFinished), leaving
// the existing Enter path to commit it. That reuse is why it doesn't duplicate
// any of the add/edit commit logic.
func (m *model) openEditorForInput() tea.Cmd {
	if err := writeNotesFile(editorDraftKey, m.textInput.Value()); err != nil {
		m.flashError(fmt.Sprintf("Error writing draft file: %v", err))
		return clearErrAfter()
	}

	editorCmd := resolveEditorCmd()
	if editorCmd == "" {
		if runtime.GOOS == "windows" {
			m.flashError(tr("No editor found. Set EDITOR permanently, e.g: setx EDITOR notepad (then restart tjek)"))
		} else {
			m.flashError(tr("No editor found. Set $EDITOR permanently, e.g: echo 'set -Ux EDITOR /usr/lib/helix/hx' >> ~/.config/fish/config.fish"))
		}
		return clearErrAfter()
	}

	m.editorTaskID = editorDraftKey
	m.editorToInput = true
	return execEditor(editorCmd, editorDraftKey, false)
}

func execEditor(editorCmd, taskID string, isFallback bool) tea.Cmd {
	c := exec.Command(editorCmd, notesFilePath(taskID))
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return editorFinishedMsg{taskID: taskID, err: err, fallback: isFallback}
	})
}

func (m model) handleEditorFinished(msg editorFinishedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		// On Windows, fall back to notepad once if the configured editor failed.
		if runtime.GOOS == "windows" && !msg.fallback {
			if notepad, lookErr := exec.LookPath("notepad"); lookErr == nil {
				m.flashError(tr("Editor failed; falling back to notepad"))
				return m, tea.Batch(clearErrAfter(), execEditor(notepad, msg.taskID, true))
			}
		}
		m.flashError(fmt.Sprintf("Editor exited with error: %v", msg.err))
		return m, clearErrAfter()
	}
	taskID := msg.taskID
	content, err := readNotesFile(taskID)
	if err != nil {
		m.flashError(fmt.Sprintf("Error reading description: %v", err))
		return m, clearErrAfter()
	}

	// ctrl+e escape hatch: the content is a comment draft, not notes —
	// reload it into the active input (collapsing the editor's newlines, since
	// these are single-line fields) and let Enter commit it as usual.
	if m.editorToInput {
		m.editorToInput = false
		cleanupNotesFile(taskID)
		m.editorTaskID = ""
		v := strings.TrimSpace(content)
		v = strings.ReplaceAll(v, "\r\n", " ")
		v = strings.ReplaceAll(v, "\n", " ")
		m.textInput.SetValue(v)
		m.textInput.CursorEnd()
		return m, nil
	}

	if t := m.get(taskID); t != nil {
		newNotes := strings.TrimRight(content, "\n\r ")
		if newNotes != t.Notes {
			m.pushUndo("edit description", t.ID)
			t.SetNotes(newNotes)
			m.markDirty(t.ID)
			m.dirty = true
			m.cache.dirty = true
			m.invalidateDetailCache()
			m.refreshCaches()
		}
	}

	cleanupNotesFile(taskID)
	m.editorTaskID = ""

	if m.dirty {
		m.dirty = false
		m.savePending = true
		if !m.saveScheduled {
			m.saveScheduled = true
			return m, scheduleSave()
		}
	}
	return m, nil
}

// ── Undo action ───────────────────────────────────────────────────────────────

func (m *model) performUndo() tea.Cmd {
	entry, ok := m.popUndo()
	if !ok {
		m.flashInfo(tr("Nothing to undo"))
		return clearErrAfter()
	}
	// Partial entries name the IDs they touched. Tasks captured in the entry
	// are restored to their prior state (mark dirty). Tasks named but not
	// captured were newly-created — undo means delete them (mark tombstone).
	// Any tombstones in the current save set are cleared for restored IDs.
	if entry.partial != nil || entry.ids != nil {
		captured := make(map[string]struct{}, len(entry.partial))
		for i := range entry.partial {
			captured[entry.partial[i].ID] = struct{}{}
		}
		var restored, removed []string
		for _, id := range entry.ids {
			if _, ok := captured[id]; ok {
				restored = append(restored, id)
			} else {
				removed = append(removed, id)
			}
		}
		m.restoreFromUndo(entry)
		m.touchRestored(restored)
		for _, id := range removed {
			m.markTombstone(id)
		}
		m.markModified(restored...)
		m.flashSuccess(fmt.Sprintf(tr("Undid: %s"), entry.desc))
		return clearErrAfter()
	}

	// Full snapshot fallback: compute the set difference and tombstone IDs
	// that existed before the undo but vanish from the restored snapshot.
	before := make(map[string]struct{}, len(m.tasks))
	for id := range m.tasks {
		before[id] = struct{}{}
	}
	m.restoreFromUndo(entry)
	restoredIDs := make([]string, 0, len(m.tasks))
	for id := range m.tasks {
		restoredIDs = append(restoredIDs, id)
		delete(before, id)
	}
	m.touchRestored(restoredIDs)
	for id := range before {
		m.markTombstone(id)
	}
	m.markModified(restoredIDs...)
	m.flashSuccess(fmt.Sprintf(tr("Undid: %s"), entry.desc))
	return clearErrAfter()
}

// touchRestored stamps a fresh ModifiedAt on each task an undo just restored
// and clears any pending tombstone for it — a restoration overrides a deletion
// that has not been flushed yet. (Clearing happens here, after the stamp, so
// the clamp below can still read the deletion's event time.) The
// restored state carries its original (old) ModifiedAt, and the sync merge is
// last-writer-wins by that timestamp — without the bump, the state being undone
// (a delete's tombstone with a newer DeletedAt, or an already-synced edit with a
// newer ModifiedAt) wins the merge and silently re-applies itself on the next
// sync. Stamping now makes the undo the latest writer, so it propagates.
func (m *model) touchRestored(ids []string) {
	for _, id := range ids {
		if t := m.get(id); t != nil {
			// Clamp against the deletion's own event time, not just the
			// restored snapshot's ModifiedAt: "now" is not reliably later
			// than the delete it undoes. Windows' clock ticks about every
			// 15ms, so a delete and the undo two keystrokes later routinely
			// read the *same* instant; an exact event-time tie resolves by
			// content hash (laterWins), a coin flip the restore could lose on
			// a device that already received the tombstone. Two sources, both
			// clamped: the pending in-memory tombstone (the moment the user
			// pressed delete, still un-flushed) and the row's deleted_at if
			// the debounced save already wrote it — whichever is later.
			prev := t.ModifiedAt
			if d := m.tombstones[id]; d.After(prev) {
				prev = d
			}
			if d := tombstoneDeletedAt(db, t.ID); d.After(prev) {
				prev = d
			}
			t.ModifiedAt = todo.StampModified(prev)
		}
		delete(m.tombstones, id)
	}
}

// ── Help overlay ──────────────────────────────────────────────────────────────

func (m model) updateHelp(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	// While typing a filter the printable keys belong to it, so the overlay's
	// own single-letter keys are off the table until enter or esc ends the
	// typing. Scrolling stays live — narrowing and then scrolling the result is
	// the normal way to use this.
	if m.helpFiltering {
		switch key.String() {
		case "enter":
			m.helpFiltering = false // keep the filter, hand the keys back
			return m, nil
		case "esc":
			m.helpFiltering = false
			m.helpFilter = ""
			m.helpScroll = 0
			return m, nil
		case "backspace":
			if r := []rune(m.helpFilter); len(r) > 0 {
				m.helpFilter = string(r[:len(r)-1])
			} else {
				m.helpFiltering = false
			}
			m.helpScroll = 0
			return m, nil
		case "up", "down", "pgup", "pgdown":
			// fall through to the scroll handling below
		default:
			if key.Type == tea.KeyRunes || key.String() == " " {
				m.helpFilter += string(key.Runes)
				if key.String() == " " {
					m.helpFilter += " "
				}
				m.helpScroll = 0
				return m, nil
			}
			return m, nil
		}
	}
	switch key.String() {
	case "/":
		m.helpFiltering = true
		m.helpScroll = 0
		return m, nil
	case "?", "q":
		m.mode = modeNormal
		m.helpScroll = 0
		m.helpFilter = ""
	case "esc":
		// esc peels one layer: clear an active filter first, close second.
		if m.helpFilter != "" {
			m.helpFilter = ""
			m.helpScroll = 0
			return m, nil
		}
		m.mode = modeNormal
		m.helpScroll = 0
	case "up", "k":
		// The overlay runs outside modeNormal, so dispatch's alias never
		// reaches it — and scrolling a wall of text is where a vim user reaches
		// for j/k hardest. Safe here because the filter above owns the
		// printable keys while it is open.
		m.helpScroll = clampHelpScroll(m.helpScroll-1, len(m.helpBodyLines()), m.helpViewportH())
	case "down", "j":
		m.helpScroll = clampHelpScroll(m.helpScroll+1, len(m.helpBodyLines()), m.helpViewportH())
	case "pgup":
		m.helpScroll = clampHelpScroll(m.helpScroll-m.helpViewportH(), len(m.helpBodyLines()), m.helpViewportH())
	case "pgdown", " ":
		m.helpScroll = clampHelpScroll(m.helpScroll+m.helpViewportH(), len(m.helpBodyLines()), m.helpViewportH())
	}
	return m, nil
}

// updateExplain drives the "why this rank" overlay. It shows a computed answer
// and offers nothing to edit, so every key closes it — w and esc by name, the
// rest so a stray key can't leave the user staring at a screen with no way out.
func (m model) updateExplain(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.KeyMsg); !ok {
		return m, nil
	}
	m.mode = modeNormal
	m.explainTaskID = ""
	return m, nil
}

// openExplain pins the task under the cursor and raises the overlay. Nothing to
// open when the cursor is on no task at all (an empty or fully filtered list).
// The Board keeps its selection in its own columns rather than the list cursor,
// so it answers with its own accessor.
func (m *model) openExplain() bool {
	t := m.currentTodo()
	if t == nil && m.tab == tabBoard {
		t = m.boardSelectedTask()
	}
	if t == nil {
		return false
	}
	m.explainTaskID = t.ID
	m.mode = modeExplain
	return true
}

// ── List pane ─────────────────────────────────────────────────────────────────

// foldsSubtasks reports whether ←/→ fold and unfold subtasks here: the Tasks
// list and the task lists inside a tag or project, which fold alike.
func (m model) foldsSubtasks() bool {
	if _, drilled := m.drillTaskList(); drilled {
		return true
	}
	return m.tab == tabTasks && !m.showHistory
}

func (m model) updateList(msg tea.Msg) (tea.Model, tea.Cmd) {
	// The Tasks list is served from a cache that markModified refreshes after
	// it has noted which task the cursor was on. A drill-in list has no such
	// cache — it re-derives from the store, so completing a task re-sorts the
	// list out from under the cursor before anything can anchor it. Note the
	// task here, and put the cursor back on it below unless the key was a
	// navigation key that moved the cursor on purpose.
	anchorID, anchorCursor := "", m.cursor
	// Set by a key that raises a toast but must not skip the anchor/clamp
	// bookkeeping below; returned with the model at the end.
	var flashCmd tea.Cmd
	if _, drilled := m.drillTaskList(); drilled {
		if t := m.currentTodo(); t != nil {
			anchorID = t.ID
		}
	}
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "q": // ctrl+c is handled globally in dispatch
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

		case "n":
			if m.tab == tabTasks && !m.showHistory && m.currentTodo() != nil {
				return m, m.openEditorForNotes()
			}
			if m.tab == tabMeetings {
				return m, m.startEditMeetingText("notes")
			}

		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			if t, ok := m.tabForNumberKey(key.String()); ok {
				m.switchTab(t)
			}

		case "tab":
			m.switchTab(m.boardCfg.nextTab(m.tab, 1))
		case "shift+tab":
			m.switchTab(m.boardCfg.nextTab(m.tab, -1))

		case "h":
			if m.tab == tabTags || m.tab == tabProjects {
				m.regroup(func() { m.showFinishedGroups = !m.showFinishedGroups })
			}
			if m.tab == tabTasks {
				m.showHistory = !m.showHistory
				if m.showHistory {
					m.pushFocus(stateHistory)
				} else {
					m.dropFocus(stateHistory)
				}
				m.cursor = 0
				m.listOffset = 0
			}

		case "f":
			if m.tab == tabTags {
				m.filterTasksByCurrentTag()
				return m, nil
			}
			if m.tab == tabProjects {
				m.filterTasksByCurrentProject()
				return m, nil
			}
			if m.tab == tabTasks && !m.showHistory {
				m.focusFilter = !m.focusFilter
				if m.focusFilter {
					m.pushFocus(stateFocusFilter)
				} else {
					m.dropFocus(stateFocusFilter)
				}
				m.cursor = 0
				m.listOffset = 0
				m.markFilterDirty()
			}

		case "right":
			if m.tab == tabBoard {
				m.boardMoveColumn(1)
			} else if m.tab == tabCalendar {
				m.moveCalendarDay(1)
			} else if m.tab == tabSettings {
				return m, m.settingsAdjust(+1)
			} else if m.foldsSubtasks() {
				if t := m.currentTodo(); t != nil && m.subtaskCount(t.ID) > 0 {
					m.setExpanded(t.ID, true)
				}
			}
		case "left":
			if m.tab == tabBoard {
				m.boardMoveColumn(-1)
			} else if m.tab == tabCalendar {
				m.moveCalendarDay(-1)
			} else if m.tab == tabSettings {
				return m, m.settingsAdjust(-1)
			} else if m.foldsSubtasks() {
				if t := m.currentTodo(); t != nil {
					// On a subtask: collapse the containing parent and
					// return the cursor to it, so ← always "moves out"
					// of the unfolded region.
					parentID := t.ID
					if t.ParentID != "" {
						parentID = t.ParentID
					}
					m.setExpanded(parentID, false)
					m.followTask(parentID)
				}
			}

		case " ":
			if m.tab == tabBoard && m.boardSelectedTask() != nil {
				m.mode = modeBoardCard
				return m, nil
			}

		case "H", "<", "shift+left":
			if m.tab == tabBoard {
				m.boardMoveCard(-1)
			}
		case "L", ">", "shift+right":
			if m.tab == tabBoard {
				m.boardMoveCard(1)
			}

		case "[", "]":
			if m.tab == tabCalendar {
				months := 1
				if key.String() == "[" {
					months = -1
				}
				m.calendar.selected = startOfDay(m.calendar.selected.AddDate(0, months, 0))
				m.calendar.entryCursor = 0
				m.calendar.focusTimeline = false
			}

		case "t":
			if m.tab == tabCalendar {
				m.calendar.selected = startOfDay(time.Now())
				m.calendar.entryCursor = 0
				m.calendar.focusTimeline = false
			} else if m.tab == tabTasks || m.drilledIntoTasks() {
				if t := m.currentTodo(); t != nil {
					// History view: only allow stopping a running
					// timer — a done task shouldn't accrue new tracked
					// time. Recovery path for tasks marked done while
					// the timer was still running.
					if m.showHistory && !t.IsTimerRunning() {
						return m, nil
					}
					if e := t.RunningEntry(); e != nil && time.Since(e.StartedAt) > idleThreshold {
						m.openIdlePrompt(t)
						return m, nil
					}
					// Capture t plus any currently-running other task
					// (toggleTimer stops it when starting a new one) so undo
					// can restore both sides.
					undoIDs := []string{t.ID}
					if !t.IsTimerRunning() {
						for otherID := range m.runningTimers {
							if otherID != t.ID {
								undoIDs = append(undoIDs, otherID)
							}
						}
					}
					m.pushUndo("toggle timer", undoIDs...)
					m.toggleTimer(t)
					m.markModified(t.ID)
					if !m.timerTickOn && m.needsSecondTick() {
						m.timerTickOn = true
						return m, timerTick()
					}
				}
			}

		case "D":
			// Rescheduling is the most common single edit a task gets, so it
			// is reachable from the row: same prompt and parser as the
			// detail pane's due-date field.
			if (m.tab == tabTasks && !m.showHistory) || m.drilledIntoTasks() {
				return m.startEditDueDate()
			}

		case "T":
			// Manual time entry — log work that wasn't captured by the live
			// timer. Available on the Tasks tab so the user can backfill
			// before marking a task done.
			if (m.tab == tabTasks && !m.showHistory) || m.drilledIntoTasks() {
				if t := m.currentTodo(); t != nil {
					m.pendingEntryTaskID = t.ID
					m.mode = modeAddTimeEntry
					m.textInput.SetValue("")
					m.textInput.Placeholder = tr("Time spent (45m, 1h30m) or HH:MM-HH:MM…")
					m.textInput.Focus()
					return m, textinput.Blink
				}
			}
			if m.tab == tabMeetings {
				return m, m.startEditMeetingText("transcript")
			}

		case "w":
			// Why this rank. Available wherever a task row is under the
			// cursor, including the board and the drill-in lists — the
			// question "why is this here" is asked of a row, not of a tab.
			if m.tab == tabTasks || m.tab == tabBoard || m.drilledIntoTasks() {
				if m.openExplain() {
					return m, nil
				}
			}

		case "/":
			return m.startSearch()

		case "s":
			m.cycleSortMode()

		case "esc":
			m.popFocus()

		case "up":
			m.moveCursorUp()
		case "down":
			m.moveCursorDown()
		case "home":
			m.listJumpTop()
		case "end":
			m.listJumpBottom()
		case "pgup":
			m.listPage(-1)
		case "pgdown":
			m.listPage(1)

		case "enter":
			return m.handleListEnter()

		case "r":
			if m.tab == tabMeetings {
				return m.handleMeetingsToggleRecord()
			}
			return m.handleListRename()

		case "m":
			// Merge tag — Tags tab only. Opens the same editor as rename but
			// with an empty input and a "Merge into…" placeholder, so the
			// user understands they're picking a *target* tag. The save
			// path (renameTagGlobally) is merge-aware: if the typed name
			// matches an existing tag, all tasks switch over and the source
			// tag disappears.
			if m.tab == tabTags {
				if tags := m.getFilteredTagsForTab(); m.tagTabCursor < len(tags) && tags[m.tagTabCursor] != untaggedKey {
					m.editingTagName = tags[m.tagTabCursor]
					m.mode = modeEditTag
					m.textInput.SetValue("")
					m.textInput.Placeholder = fmt.Sprintf(tr("Merge #%s into…"), tags[m.tagTabCursor])
					m.textInput.Focus()
					return m, textinput.Blink
				}
			}

		case "x", "delete":
			if m.tab == tabMeetings {
				return m.handleMeetingsDeleteOrReject()
			}
			return m.handleListDelete()

		case "g":
			if m.tab == tabMeetings {
				return m.handleMeetingsRunAI()
			}

		case "a":
			if m.tab == tabMeetings {
				m.mode = modeAddMeeting
				m.textInput.SetValue("")
				m.textInput.Placeholder = tr("Meeting title...")
				m.textInput.Focus()
				return m, textinput.Blink
			}
			// Quick-add, pre-seeded with the grouping you are standing in: on
			// the Tags/Projects tab the new task lands in the tag or project
			// you are looking at, so capturing into it costs one key instead
			// of a tab switch and a remembered spelling.
			if seed, ok := m.quickAddSeed(); ok {
				if m.tab == tabBoard {
					m.board.addCol, _ = m.boardSelection(m.boardColumns())
				}
				m.mode = modeInput
				m.textInput.SetValue(seed)
				m.textInput.SetCursor(len([]rune(seed)))
				// Syntax lives in the persistent hint line under the input
				// (buildFooterContent) — a placeholder vanishes on the first
				// keystroke, exactly when the syntax reference is needed.
				m.textInput.Placeholder = tr("New task...")
				m.textInput.Focus()
				return m, textinput.Blink
			}

		case "d":
			if m.tab == tabTasks || m.drilledIntoTasks() {
				if t := m.currentTodo(); t != nil {
					// Un-marking a done task is a state change the user
					// rarely means (usually a stray 'd' on a completed row)
					// and it voids the completion rank — so confirm it.
					// Marking done stays immediate.
					if t.Status != todo.Pending {
						m.stageReopenConfirm(t)
						return m, nil
					}
					if !m.closePendingTask(t) {
						return m, nil // confirm staged (open subtasks)
					}
					// Subtasks stay visible after toggling (dimmed with a
					// check), so the cursor stays on the same row. Parents
					// disappear from active and the cursor would land on
					// the next row — decrement so it lands on the previous
					// one instead.
					if m.tab == tabTasks && t.ParentID == "" && m.cursor > 0 {
						m.cursor--
					}
				}
			} else if m.tab == tabBoard {
				if t := m.boardSelectedTask(); t != nil {
					if t.Status != todo.Pending {
						m.stageReopenConfirm(t)
						return m, nil
					}
					if m.closePendingTask(t) {
						m.boardFollow(m.boardCfg.doneColumn(), t.ID)
					}
				}
			}

		case "p":
			if (m.tab == tabTasks && !m.showHistory) || m.drilledIntoTasks() {
				if t := m.currentTodo(); t != nil && m.cyclePriority(t) {
					m.flashInfo(tr("A subtask can't outrank its parent"))
					flashCmd = clearErrAfter()
				}
			}
		}
	}

	if anchorID != "" && m.cursor == anchorCursor {
		m.followTask(anchorID)
	}

	return m, flashCmd
}

// ── List helper methods ───────────────────────────────────────────────────────

// quickAddSeed reports whether 'a' opens the quick-add field here, and with
// what text already in it. The Tags and Projects tabs seed the token for the
// row you are on — including while drilled into its tasks, where adding one
// more task to the same group is the obvious next move. A project whose name
// carries a space cannot round-trip through quick-add's whitespace
// tokenisation (see suggest.go), so it seeds an empty field rather than a line
// that would parse wrong.
func (m model) quickAddSeed() (string, bool) {
	switch m.tab {
	case tabTasks:
		return "", !m.showHistory
	case tabTags:
		tags := m.getFilteredTagsForTab()
		if m.tagTabCursor >= len(tags) || tags[m.tagTabCursor] == untaggedKey {
			return "", true
		}
		return "#" + tags[m.tagTabCursor] + " ", true
	case tabBoard:
		return "", true
	case tabProjects:
		projects := m.allProjectsForList()
		if m.projectCursor >= len(projects) || strings.ContainsFunc(projects[m.projectCursor], unicode.IsSpace) {
			return "", true
		}
		return "@" + projects[m.projectCursor] + " ", true
	}
	return "", false
}

// startEditTaskTitle opens the rename editor for the task under the cursor —
// shared by the Tasks tab and both drill-in lists, so a task is renamed the
// same way wherever you meet it.
// startEditDueDate opens the date prompt for the task under the cursor. The
// prompt, the parser and the error message are the detail pane's — a due date
// typed from the list has to behave exactly like one typed from the field.
func (m model) startEditDueDate() (tea.Model, tea.Cmd) {
	t := m.currentTodo()
	if t == nil {
		return m, nil
	}
	m.mode = modeEditDue
	if t.DueDate.IsZero() {
		m.textInput.SetValue("")
	} else {
		m.textInput.SetValue(t.DueDate.Format("02-01-06"))
	}
	m.textInput.Placeholder = tr("Due date (dd-mm-yy, 'today', 'next week', '+3d')...")
	m.textInput.Focus()
	return m, textinput.Blink
}

func (m model) startEditTaskTitle() (tea.Model, tea.Cmd) {
	t := m.currentTodo()
	if t == nil {
		return m, nil
	}
	m.mode = modeEditTitle
	m.textInput.SetValue(t.Title)
	m.textInput.Placeholder = tr("Edit task title...")
	m.textInput.Focus()
	return m, textinput.Blink
}

// stageDeleteTask stages the delete confirm for the task under the cursor,
// counting the subtasks that would go with it.
func (m model) stageDeleteTask() (tea.Model, tea.Cmd) {
	t := m.currentTodo()
	if t == nil {
		return m, nil
	}
	m.mode = modeConfirm
	m.confirmOnYes = (*model).confirmDeleteTask
	m.pendingDeleteID = t.ID
	if n := len(m.descendantIDs(t.ID)) - 1; n > 0 {
		m.confirmMsg = fmt.Sprintf(tr("Delete '%s' and %d subtask(s)? (y/n)"), t.Title, n)
	} else {
		m.confirmMsg = fmt.Sprintf(tr("Delete '%s'? (y/n)"), t.Title)
	}
	return m, nil
}

// clampCursors keeps every list cursor addressing a row that exists. Lists
// shrink out from under their cursor constantly — an undo that removes the task
// you were on, a delete confirmed from a modal, a filter narrowing, a tab
// switch restoring a cursor saved when the list was longer — and a cursor past
// the end selects nothing, which turns the next keystroke into a silent no-op
// (press d, nothing happens). The movement keys wrap modulo the length, so they
// recover on their own; everything else needs this.
//
// Called once per dispatch, after the handlers, so no individual mutation has
// to remember to clamp. Cursors that cannot go stale (the Settings rows are
// stable IDs; the board clamps at selection time) are not listed.
func (m *model) clampCursors() {
	clamp := func(cursor *int, n int) {
		if *cursor >= n {
			*cursor = n - 1
		}
		if *cursor < 0 {
			*cursor = 0
		}
	}
	// The tag and project cursors are tab-private state that survives a detour
	// to another tab, so they can go stale while you are elsewhere — deleting a
	// task's last tag from the Tasks tab shortens the tag list. Both lists are
	// cached lookups, so clamp them regardless of the live tab. The calendar's
	// entry cursor needs an uncached scan, so it is only clamped where it is
	// actually in use.
	m.followPinnedGroups()
	clamp(&m.tagTabCursor, len(m.getFilteredTagsForTab()))
	clamp(&m.projectCursor, len(m.allProjectsForList()))
	// Before the drill's early return: a task opened from a drill list has
	// the detail pane too.
	m.clampDetailScroll()
	if tasks, drilled := m.drillTaskList(); drilled {
		clamp(&m.cursor, len(tasks))
		switch {
		case m.drillDetailOpen():
			// The list beside an opened task is the list panel, windowed by
			// listOffset, which the tag or project list was using a moment ago.
			m.clampListOffsetVisible(m.cursor, len(tasks), m.drillTaskVisibleRows())
		case m.tab == tabTags:
			// The group's list sits in the pane, which windows itself around
			// the cursor; listOffset stays the group list's above it.
			m.clampListOffsetVisible(m.tagTabCursor, len(m.getFilteredTagsForTab()), m.tagListVisibleRows())
		default:
			m.clampListOffsetVisible(m.projectCursor, len(m.allProjectsForList()), m.projectListVisibleRows())
		}
		return // the drill owns m.cursor while it is open
	}
	// Turning the server off takes its detail rows off the pane; a cursor left
	// on one addresses a row nobody can see, and the next keypress edits it.
	if !m.settingsRowVisible(m.settingsCursor) || !settingsSelectable(m.settingsCursor) {
		m.settingsCursor = settingServerOn
	}
	switch m.tab {
	case tabTags:
		m.clampListOffsetVisible(m.tagTabCursor, len(m.getFilteredTagsForTab()), m.tagListVisibleRows())
	case tabProjects:
		m.clampListOffsetVisible(m.projectCursor, len(m.allProjectsForList()), m.projectListVisibleRows())
	case tabTasks:
		clamp(&m.cursor, m.currentTaskListLen())
		// Against the rows the list really shows, which change with more than
		// the cursor: a detail opened under the list, a timer line, a prompt.
		m.clampListOffsetVisible(m.cursor, m.currentTaskListLen(), m.taskListRows())
	case tabCalendar:
		clamp(&m.calendar.entryCursor, len(m.activitiesForDay(m.calendar.selected)))
	case tabBoard:
		m.clampBoardWindow()
	}
}

// clampBoardWindow scrolls the visible column range just enough to keep the
// focused column on screen. ←/→ move the focus; the view comes along only when
// the focus would leave it, which is what makes the board scroll a column at a
// time rather than jumping a page. Clamped here, with every other cursor, so
// an offset left stale by a stage-list edit or a filter degrades to a valid
// window instead of rendering past the end.
func (m *model) clampBoardWindow() {
	cols := m.boardColumns()
	_, count, _ := boardWindow(len(cols), m.board.colOffset, m.termWidth-8)
	if count == 0 || count >= len(cols) {
		m.board.colOffset = 0 // everything fits (or nothing does): no window
		m.clampBoardCardScroll()
		return
	}
	col, _ := m.boardSelection(cols)
	if col < m.board.colOffset {
		m.board.colOffset = col
	}
	if col >= m.board.colOffset+count {
		m.board.colOffset = col - count + 1
	}
	if max := len(cols) - count; m.board.colOffset > max {
		m.board.colOffset = max
	}
	if m.board.colOffset < 0 {
		m.board.colOffset = 0
	}
	m.clampBoardCardScroll()
}

// clampBoardCardScroll keeps the selected card inside the focused column's
// drawn window, scrolling only as far as that takes. Focus moving to another
// column starts that column from the top.
func (m *model) clampBoardCardScroll() {
	cols := m.boardColumnsForView()
	col, cursor := m.boardSelection(cols)
	if col != m.board.scrollCol {
		m.board.scrollCol, m.board.cardScroll = col, 0
	}
	g := m.boardGeometry(cols)
	if g.count == 0 || col < g.start || col >= g.start+g.count || len(cols[col]) == 0 {
		m.board.cardScroll = 0
		return
	}
	heights := boardCardHeights(cols[col], g.widths[col-g.start], g.layouts[col-g.start])
	m.board.cardScroll, _ = boardCardWindow(heights, cursor, m.board.cardScroll, g.budget-2)
}

// drilledIntoTasks reports whether the cursor has walked into a row's task
// list on the Tags or Projects tab. The row-level keys (d/t/p/T/r/x) gate on
// it, so "the cursor is on a task" has one answer instead of a per-key list of
// tabs that drifts apart.
func (m model) drilledIntoTasks() bool {
	_, drilled := m.drillTaskList()
	return drilled
}

// filterTasksByCurrentTag jumps to the Tasks tab filtered by the tag under the
// Tags-tab cursor — the tab's old enter behaviour, now on f, since enter drills
// into the tag's tasks instead.
func (m *model) filterTasksByCurrentTag() {
	tags := m.getFilteredTagsForTab()
	if m.tagTabCursor >= len(tags) {
		return
	}
	tag := tags[m.tagTabCursor]
	m.switchTab(tabTasks)
	// A fresh filter, so start at the top rather than the Tasks cursor
	// switchTab just restored.
	if tag == untaggedKey {
		m.searchQuery = untaggedKey
	} else {
		m.searchQuery = "#" + tag
	}
	// switchTab already moved us to Tasks, so the entry lands on the tab the
	// search filters.
	m.pushFocus(stateSearch)
	m.cursor = 0
	m.listOffset = 0
	m.markFilterDirty()
	m.persistSettings()
}

// filterTasksByCurrentProject is f on the Projects tab: the Tasks tab, filtered
// to the project under the cursor. The search grammar splits on spaces, so a
// project named with several words is found by its first one.
func (m *model) filterTasksByCurrentProject() {
	projects := m.allProjectsForList()
	if m.projectCursor >= len(projects) {
		return
	}
	project := strings.Fields(projects[m.projectCursor])[0]
	m.switchTab(tabTasks)
	m.searchQuery = "@" + project
	m.pushFocus(stateSearch)
	m.cursor = 0
	m.listOffset = 0
	m.markFilterDirty()
	m.persistSettings()
}

// tabForNumberKey maps a digit shortcut to its tab. It is the one place the
// number→tab assignment lives, so the list pane and the detail pane can't
// drift apart on it — the help advertises the digits as global, and they are.
// tabForNumberKey maps a digit to its tab. The digits never renumber when a
// tab is hidden — 6 is Stats whether or not the board is on — so a hidden
// tab's digit simply does nothing rather than silently meaning something else.
func (m model) tabForNumberKey(key string) (tab, bool) {
	if t, ok := tabForNumberKeyRaw(key); ok {
		return t, m.boardCfg.tabVisible(t)
	}
	return 0, false
}

func tabForNumberKeyRaw(key string) (tab, bool) {
	switch key {
	case "1":
		return tabTasks, true
	case "2":
		return tabCalendar, true
	case "3":
		return tabTags, true
	case "4":
		return tabProjects, true
	case "5":
		return tabBoard, true
	case "6":
		return tabStats, true
	case "7":
		return tabSettings, true
	case "8":
		return tabMeetings, true
	case "9":
		return tabChat, true
	}
	return tabTasks, false
}

// nextTab steps delta tabs from cur, wrapping in both directions — tab walks
// forward, shift+tab back.
// tabVisible reports whether a tab is reachable. Only the Board is hideable
// (Settings → "Kanban board"), and hiding it takes it out of the bar, out of
// tab/shift+tab, and off its digit — a tab you cannot see must not be one you
// can land on by accident.
func (c boardConfig) tabVisible(t tab) bool {
	return t != tabBoard || c.shown
}

func (c boardConfig) visibleTabCount() int {
	n := 0
	for i := 0; i < numTabs; i++ {
		if c.tabVisible(tab(i)) {
			n++
		}
	}
	return n
}

// nextTab steps to the next visible tab, so cycling never stops on a hidden
// one. At least Tasks is always visible, so the walk terminates.
func (c boardConfig) nextTab(cur tab, delta int) tab {
	if delta == 0 {
		return cur
	}
	step := 1
	if delta < 0 {
		step = -1
	}
	next := cur
	for i := 0; i < numTabs; i++ {
		next = tab((int(next) + step + numTabs) % numTabs)
		if c.tabVisible(next) {
			return next
		}
	}
	return cur
}

func (m *model) switchTab(t tab) {
	if t == m.tab || !m.boardCfg.tabVisible(t) {
		return
	}
	// Snapshot the leaving tab's shared UI state and restore the entering
	// tab's, so a tab switch is non-destructive: your cursor, scroll, open
	// pane, and search survive a detour to another tab. Tab-private state
	// (projectCursor, tagTabCursor, showHistory, the calendar day, …) lives in
	// its own fields and persists on its own.
	m.tabViews[m.tab] = tabView{
		cursor:       m.cursor,
		listOffset:   m.listOffset,
		pane:         m.pane,
		search:       m.searchQuery,
		detailTaskID: m.detailTaskID,
		detailStack:  m.detailStack,
	}
	// The Chat tab's textarea needs to own every keystroke the whole time
	// it's on screen (modeChatInput's doc comment in model.go), so entering
	// or leaving it means entering or leaving that mode here, rather than
	// through some explicit "start editing" key the way every other
	// in-app text editor works.
	if m.tab == tabChat {
		m.chatInput.Blur()
		m.mode = modeNormal
	}
	m.tab = t
	v := m.tabViews[t]
	m.cursor = v.cursor
	m.listOffset = v.listOffset
	m.pane = v.pane
	m.searchQuery = v.search
	m.detailTaskID = v.detailTaskID
	m.detailStack = v.detailStack
	if t == tabChat {
		m.mode = modeChatInput
		m.chatInput.Focus()
	}

	m.invalidateDetailCache()
	m.markFilterDirty()
}

// startEditTimeEntry opens the inline editor for an entry's times,
// prefilled with the current range.
func (m *model) startEditTimeEntry(taskID, entryID string) tea.Cmd {
	t := m.findTodoByID(taskID)
	if t == nil {
		return nil
	}
	for i := range t.TimeEntries {
		if t.TimeEntries[i].ID == entryID {
			e := &t.TimeEntries[i]
			val := e.StartedAt.Format("15:04") + "-"
			if e.IsRunning() {
				val += "now"
			} else {
				val += e.StoppedAt.Format("15:04")
			}
			m.pendingEntryTaskID = taskID
			m.pendingEntryID = entryID
			m.mode = modeEditTimeEntry
			m.textInput.SetValue(val)
			m.textInput.Placeholder = tr("HH:MM-HH:MM or duration (45m, 1h30m)...")
			m.textInput.Focus()
			return textinput.Blink
		}
	}
	return nil
}

func (m *model) moveCalendarDay(days int) {
	m.calendar.selected = m.calendar.selected.AddDate(0, 0, days)
	m.calendar.entryCursor = 0
	m.calendar.focusTimeline = false
}

func (m model) startSearch() (tea.Model, tea.Cmd) {
	switch m.tab {
	case tabTags:
		m.mode = modeSearchTagTab
		m.tagTabSearchInput.SetValue("")
		m.tagTabSearchQuery = ""
		m.tagTabCursor = 0
		m.tagTabSearchInput.Focus()
		return m, textinput.Blink
	case tabTasks, tabProjects, tabStats, tabBoard:
		// Stats and Board share the Tasks-list query. Both are projections of
		// the same filtered lists — renderStatsList aggregates the matching
		// top-level tasks, buildBoardColumns deals out cache.active/cache.done
		// — so a #tag or @project search scopes the whole page for free, and
		// the status line shows the same chip on every tab.
		m.mode = modeSearch
		m.searchInput.SetValue("")
		m.searchInput.Focus()
		return m, textinput.Blink
	}
	return m, nil
}

func (m *model) cycleSortMode() {
	switch m.tab {
	case tabTags:
		if m.tagTaskMode {
			return
		}
		m.regroup(func() { m.tagOrder = m.tagOrder.next() })
	case tabProjects:
		if m.projectTaskMode {
			return
		}
		m.regroup(func() { m.projectOrder = m.projectOrder.next() })
	case tabTasks:
		if m.showHistory {
			// History has its own two-state cycle: Completed → Alpha.
			if m.historySort == historySortCompleted {
				m.historySort = historySortAlpha
			} else {
				m.historySort = historySortCompleted
			}
		} else {
			// Three-state cycle: Sequence → DueDate → Size → Sequence.
			switch m.taskSort {
			case taskSortSequence:
				m.taskSort = taskSortDueDate
			case taskSortDueDate:
				m.taskSort = taskSortSize
			default:
				m.taskSort = taskSortSequence
			}
		}
		m.cursor = 0
		m.listOffset = 0
		m.markCacheDirty()
	}
	m.persistSettings()
}

// regroup applies a change to how the Tags and Projects lists are drawn — a new
// order, finished groups shown or hidden — and keeps both cursors on the group
// they were on, which a new order has usually moved.
func (m *model) regroup(change func()) {
	tags, projects := m.getFilteredTagsForTab(), m.allProjectsForList()
	tag, project := "", ""
	if m.tagTabCursor < len(tags) {
		tag = tags[m.tagTabCursor]
	}
	if m.projectCursor < len(projects) {
		project = projects[m.projectCursor]
	}
	change()
	if i := slices.Index(m.getFilteredTagsForTab(), tag); i >= 0 {
		m.tagTabCursor = i
	}
	if i := slices.Index(m.allProjectsForList(), project); i >= 0 {
		m.projectCursor = i
	}
}

// followPinnedGroups keeps a drilled-in cursor on the group it drilled into.
// The list re-sorts under it whenever a task inside changes its counts, and
// the index alone would then open a different group.
func (m *model) followPinnedGroups() {
	if m.tagTaskMode {
		if i := slices.Index(m.getFilteredTagsForTab(), m.tagPinned); i >= 0 {
			m.tagTabCursor = i
		}
	}
	if m.projectTaskMode {
		if i := slices.Index(m.allProjectsForList(), m.projectPinned); i >= 0 {
			m.projectCursor = i
		}
	}
}

// isBiasSettingRow reports whether the given Settings cursor row is one of
// the three sequencing-bias knobs (so ←/→ should cycle a bias rather than the
// theme/language picker).
func (m *model) isBiasSettingRow(row int) bool {
	return row == settingBiasDeadline || row == settingBiasPriority || row == settingBiasMomentum
}

// cycleBias rotates the named bias by `direction` (+1 next, -1 prev), updates
// the model's ranker, invalidates the sort cache so the new ranking takes
// effect on the next render, persists the change, and resyncs the persisted
// `sequence` column so anything reading the database directly sees the new
// weights immediately rather than waiting for the next mutation.
func (m *model) cycleBias(row, direction int) {
	switch row {
	case settingBiasDeadline:
		m.rank.Biases.Deadline = rank.CycleLevel(m.rank.Biases.Deadline, direction)
	case settingBiasPriority:
		m.rank.Biases.Priority = rank.CycleLevel(m.rank.Biases.Priority, direction)
	case settingBiasMomentum:
		m.rank.Biases.Momentum = rank.CycleLevel(m.rank.Biases.Momentum, direction)
	default:
		return
	}
	m.markCacheDirty()
	m.persistSettings()
	m.repo.SetRanker(m.rank)
	if err := m.repo.ResyncScores(); err != nil {
		m.flashError(fmt.Sprintf(tr("Score resync failed: %v"), err))
	}
}

// toggleAging flips the Aging contribution on/off, mirroring cycleBias's
// invalidate-persist-resync pattern so the new ranking is visible immediately
// and persists across restarts.
func (m *model) toggleAging() {
	m.rank.Biases.Aging = !m.rank.Biases.Aging
	m.markCacheDirty()
	m.persistSettings()
	m.repo.SetRanker(m.rank)
	if err := m.repo.ResyncScores(); err != nil {
		m.flashError(fmt.Sprintf(tr("Score resync failed: %v"), err))
	}
}

// toggleAutoCloseParent flips the "close parent when all subtasks done"
// preference. Doesn't retroactively close already-complete subtrees — only
// future subtask transitions trigger the auto-close.
func (m *model) toggleAutoCloseParent() {
	m.autoCloseParent = !m.autoCloseParent
	m.persistSettings()
}

// toggleAutoCloseSubtasks flips the "close subtasks when the parent is closed"
// preference. Like its mirror, it's forward-only — it doesn't retroactively
// close subtasks already stranded under a done parent.
func (m *model) toggleAutoCloseSubtasks() {
	m.autoCloseSubtasks = !m.autoCloseSubtasks
	m.persistSettings()
}

// toggleSyncBoard turns sharing the column list with the fleet on or off.
// Turning it on does not push anything by itself: this device's list only
// wins if it was edited here more recently than the fleet's (boardsync.go).
func (m *model) toggleSyncBoard() {
	m.boardCfg.sync = !m.boardCfg.sync
	m.persistSettings()
}

// toggleShowBoard turns the kanban surface on and off. Turning it off while
// standing on the Board would leave the cursor on a tab that no longer exists
// in the bar, so the move off happens here rather than being discovered by the
// next keystroke.
func (m *model) toggleShowBoard() {
	m.boardCfg.shown = !m.boardCfg.shown
	if !m.boardCfg.shown && m.tab == tabBoard {
		m.switchTab(tabTasks)
	}
	// The Stage row appears and disappears with it, and the detail pane caches
	// its rendered lines.
	m.invalidateDetailCache()
	m.persistSettings()
	m.markCacheDirty()
}

// persistedSearch is the search query settings.json keeps: the Tasks tab's,
// wherever the cursor happens to be. The query is shared by Tasks, Board and
// Stats but stored per tab in tabViews, and only the leaving tab's copy is
// snapshotted there — so on any other tab the live m.searchQuery belongs to
// that tab, and the Tasks one is the snapshot switchTab left behind.
func (m *model) persistedSearch() string {
	if m.tab == tabTasks {
		return m.searchQuery
	}
	return m.tabViews[tabTasks].search
}

// persistSettings writes all current preferences to disk, surfacing any write
// failure so a setting that silently won't stick is at least visible.
func (m *model) persistSettings() {
	if err := saveSettings(appSettings{
		TaskSort:          m.taskSort,
		HistorySort:       m.historySort,
		TagOrder:          m.tagOrder,
		ProjectOrder:      m.projectOrder,
		Theme:             m.themeName,
		Language:          string(activeLang),
		SeqBiasDeadline:   m.rank.Biases.Deadline,
		SeqBiasPriority:   m.rank.Biases.Priority,
		SeqBiasMomentum:   m.rank.Biases.Momentum,
		SeqAgingDisabled:  !m.rank.Biases.Aging,
		AutoCloseParent:   m.autoCloseParent,
		AutoCloseSubtasks: m.autoCloseSubtasks,
		BoardDisabled:     !m.boardCfg.shown,
		Stages:            m.boardCfg.stages,
		StageIcons:        m.boardCfg.icons,
		StagesModifiedAt:  m.boardCfg.modifiedAt,
		SyncBoardDisabled: !m.boardCfg.sync,
		Search:            m.persistedSearch(),
		DetailPosition:    m.detailPos.String(),
		Reminder:          formatReminder(m.reminderAt),
		Keys:              activeKeys,

		SubtaskTagsDisabled: !m.subtaskTags,
		ReminderOff:         !m.reminderOn,
		ExportFolder:        m.exportFolder,

		AIProvider:   m.aiProvider,
		AnthropicKey: m.aiKeys.Anthropic,
		OpenAIKey:    m.aiKeys.OpenAI,
		GeminiKey:    m.aiKeys.Gemini,
		MistralKey:   m.aiKeys.Mistral,
		FFmpegInput:  m.ffmpegInput,

		UseLocalWhisper:    m.useLocalWhisper,
		WhisperBinOverride: m.whisperBinOverride,
		WhisperLanguage:    m.whisperLanguage,
	}); err != nil {
		m.flashError(fmt.Sprintf(tr("Error saving settings: %v"), err))
	}
}

func (m *model) moveCursorUp() {
	switch m.tab {
	case tabBoard:
		m.boardMoveCursor(-1)
	case tabCalendar:
		if m.calendar.focusTimeline {
			if m.calendar.entryCursor > 0 {
				m.calendar.entryCursor--
			}
		} else {
			m.moveCalendarDay(-7)
		}
	case tabTags:
		if m.tagTaskMode {
			if n := len(m.currentTagTasks()); n > 0 {
				m.cursor = (m.cursor - 1 + n) % n
			}
		} else if n := len(m.getFilteredTagsForTab()); n > 0 {
			m.tagTabCursor = (m.tagTabCursor - 1 + n) % n
		}
	case tabSettings:
		m.settingsCursor = m.settingsCursorStep(m.settingsCursor, -1)
	case tabProjects:
		if m.projectTaskMode {
			if n := m.currentProjectTaskLen(); n > 0 {
				m.cursor = (m.cursor - 1 + n) % n
			}
		} else if n := len(m.allProjectsForList()); n > 0 {
			m.projectCursor = (m.projectCursor - 1 + n) % n
			m.cursor = 0
			m.listOffset = 0
		}
	case tabTasks:
		if n := m.currentTaskListLen(); n > 0 {
			m.cursor = (m.cursor - 1 + n) % n
		}
	case tabStats:
		m.statsScroll = max(0, min(m.statsScroll, m.statsMaxScroll())-1)
	case tabMeetings:
		m.moveMeetingsCursor(-1)
	}
}

func (m *model) moveCursorDown() {
	switch m.tab {
	case tabBoard:
		m.boardMoveCursor(1)
	case tabCalendar:
		if m.calendar.focusTimeline {
			if acts := m.activitiesForDay(m.calendar.selected); m.calendar.entryCursor < len(acts)-1 {
				m.calendar.entryCursor++
			}
		} else {
			m.moveCalendarDay(7)
		}
	case tabTags:
		if m.tagTaskMode {
			if n := len(m.currentTagTasks()); n > 0 {
				m.cursor = (m.cursor + 1) % n
			}
		} else if n := len(m.getFilteredTagsForTab()); n > 0 {
			m.tagTabCursor = (m.tagTabCursor + 1) % n
		}
	case tabSettings:
		m.settingsCursor = m.settingsCursorStep(m.settingsCursor, +1)
	case tabProjects:
		if m.projectTaskMode {
			if n := m.currentProjectTaskLen(); n > 0 {
				m.cursor = (m.cursor + 1) % n
			}
		} else if n := len(m.allProjectsForList()); n > 0 {
			m.projectCursor = (m.projectCursor + 1) % n
			m.cursor = 0
			m.listOffset = 0
		}
	case tabTasks:
		if n := m.currentTaskListLen(); n > 0 {
			m.cursor = (m.cursor + 1) % n
		}
	case tabStats:
		m.statsScroll = min(m.statsScroll+1, m.statsMaxScroll())
	case tabMeetings:
		m.moveMeetingsCursor(1)
	}
}

// listNavTarget abstracts the current tab's linear list for the jump/page keys
// (Home/End/PgUp/PgDn): a pointer to the tab's cursor and the row count.
// Returns (nil, 0) for tabs without a simple linear list (calendar, settings,
// stats), where those keys are a no-op.
func (m *model) listNavTarget() (*int, int) {
	switch m.tab {
	case tabTasks:
		return &m.cursor, m.currentTaskListLen()
	case tabTags:
		if m.tagTaskMode {
			return &m.cursor, len(m.currentTagTasks())
		}
		return &m.tagTabCursor, len(m.getFilteredTagsForTab())
	case tabProjects:
		if m.projectTaskMode {
			return &m.cursor, m.currentProjectTaskLen()
		}
		return &m.projectCursor, len(m.allProjectsForList())
	}
	return nil, 0
}

// moveListCursorTo clamps target into range and applies it to the current tab's
// list cursor. In the Projects list (not task mode) picking a different project
// resets the task sub-cursor + its scroll, mirroring moveCursorUp/Down.
func (m *model) moveListCursorTo(target int) {
	c, n := m.listNavTarget()
	if c == nil || n == 0 {
		return
	}
	if target < 0 {
		target = 0
	}
	if target > n-1 {
		target = n - 1
	}
	if m.tab == tabProjects && !m.projectTaskMode && target != *c {
		m.cursor = 0
		m.listOffset = 0
	}
	*c = target
}

// listPageStep is roughly one visible page for PgUp/PgDn, keeping one row of
// overlap for context.
func (m *model) listPageStep() int {
	step := m.listVisible() - 1
	if _, drilled := m.drillTaskList(); drilled || m.tab == tabTasks {
		step = m.taskListRows() - 1
	}
	if step < 1 {
		step = 1
	}
	return step
}

func (m *model) listJumpTop() { m.moveListCursorTo(0) }

func (m *model) listJumpBottom() {
	_, n := m.listNavTarget()
	m.moveListCursorTo(n - 1)
}

func (m *model) listPage(dir int) {
	if c, _ := m.listNavTarget(); c != nil {
		m.moveListCursorTo(*c + dir*m.listPageStep())
	}
}

// currentTaskListLen is the row count of the active Tasks list — the history
// list when showing history, otherwise the visible active list.
func (m *model) currentTaskListLen() int {
	if m.showHistory {
		return len(m.cache.done)
	}
	return m.visibleActiveLen()
}

// currentProjectTaskLen is the task count of the project under the project
// cursor (used while drilled into a project's task list).
func (m *model) currentProjectTaskLen() int {
	projects := m.allProjectsForList()
	if m.projectCursor < 0 || m.projectCursor >= len(projects) {
		return 0
	}
	return len(m.getProjectTasks(projects[m.projectCursor]))
}

func (m model) handleListEnter() (tea.Model, tea.Cmd) {
	switch m.tab {
	case tabMeetings:
		return m.handleMeetingsEnter()
	case tabBoard:
		m.startBoardCarry()
	case tabCalendar:
		if len(m.activitiesForDay(m.calendar.selected)) > 0 {
			m.calendar.focusTimeline = true
			m.calendar.entryCursor = 0
			m.pushFocus(stateCalTimeline)
		}
	case tabProjects:
		if !m.projectTaskMode {
			if projects := m.allProjectsForList(); m.projectCursor < len(projects) {
				m.projectPinned = projects[m.projectCursor]
				m.projectTaskMode = true
				m.cursor = 0
				m.pushFocus(stateProjectDrill)
			}
		} else if t := m.currentTodo(); t != nil {
			m.detailTaskID = t.ID
			m.detailStack = nil
			m.pane = paneDetail
			m.detail = detailState{field: fieldStartDate}
			m.invalidateDetailCache()
			m.pushFocus(stateDetailPane)
		}
	case tabTasks:
		if t := m.currentTodo(); t != nil {
			m.detailTaskID = t.ID
			m.detailStack = nil
			m.pane = paneDetail
			m.detail = detailState{field: fieldStartDate}
			m.invalidateDetailCache()
			m.pushFocus(stateDetailPane)
		}
	case tabTags:
		// Enter walks in one level at a time, exactly like the Projects tab:
		// tag → its tasks → the selected task's detail. Filtering the Tasks
		// tab by this tag is f.
		if !m.tagTaskMode {
			if tags := m.getFilteredTagsForTab(); m.tagTabCursor < len(tags) {
				m.tagPinned = tags[m.tagTabCursor]
				m.tagTaskMode = true
				m.cursor = 0
				m.pushFocus(stateTagDrill)
			}
		} else if t := m.currentTodo(); t != nil {
			m.detailTaskID = t.ID
			m.detailStack = nil
			m.pane = paneDetail
			m.detail = detailState{field: fieldStartDate}
			m.invalidateDetailCache()
			m.pushFocus(stateDetailPane)
		}
	case tabStats:
		if m.statsChartShown() {
			m.statsRange = (m.statsRange + 1) % statsRangeCount
		}
	case tabSettings:
		return m.handleSettingsEnter()
	}
	return m, nil
}

// ── Settings tab ──────────────────────────────────────────────────────────────

func (m model) handleSettingsEnter() (tea.Model, tea.Cmd) {
	switch m.settingsCursor {
	case settingExportFolder:
		return m.openExportFolderEditor()
	case settingImportFile:
		return m.openImportPrompt()
	case settingStages:
		m.mode = modeEditStages
		m.textInput.SetValue(m.boardCfg.stagesDisplay())
		m.textInput.Placeholder = tr("Board columns, comma-separated")
		m.textInput.Focus()
		return m, textinput.Blink
	case settingSyncServer:
		m.mode = modeEditSyncURL
		m.textInput.SetValue(m.syncCfg.URL)
		m.textInput.Placeholder = tr("Sync server URL, e.g. http://100.x.y.z:8765")
		m.textInput.Focus()
		return m, textinput.Blink
	case settingSyncToken:
		m.mode = modeEditSyncToken
		m.textInput.SetValue(m.syncCfg.Token)
		// Mask the pre-filled secret, as the list row does. The editors reset EchoMode
		// on exit so the shared input doesn't stay masked for other modes.
		m.textInput.EchoMode = textinput.EchoPassword
		m.textInput.Placeholder = tr("Sync token (clear the field to remove it)")
		m.textInput.Focus()
		return m, textinput.Blink
	case settingSyncNow:
		if !m.syncCfg.ready() {
			m.syncStatus = tr("Set sync server + token first")
			return m, nil
		}
		m.syncStatus = tr("Syncing…")
		return m, m.backgroundSync()
	case settingServerListen:
		m.mode = modeEditServerListen
		m.textInput.SetValue(m.syncCfg.listenAddr())
		m.textInput.Placeholder = tr("Bind address, e.g. 100.x.y.z:8765 or 127.0.0.1:8765")
		m.textInput.Focus()
		return m, textinput.Blink
	case settingServerToken:
		m.mode = modeEditServerToken
		m.textInput.SetValue(m.syncCfg.ServerToken)
		m.textInput.EchoMode = textinput.EchoPassword // see settingSyncToken
		m.textInput.Placeholder = tr("Server token clients must present (ctrl+g generates one · blank removes it)")
		m.textInput.Focus()
		return m, textinput.Blink
	case settingCheckUpdate:
		m.updateStatus = tr("Checking…")
		return m, checkForUpdate()
	case settingAnthropicKey:
		m.mode = modeEditAnthropicKey
		m.textInput.SetValue(m.aiKeys.Anthropic)
		m.textInput.EchoMode = textinput.EchoPassword // see settingSyncToken
		m.textInput.Placeholder = tr("Anthropic API key (clear the field to remove it)")
		m.textInput.Focus()
		return m, textinput.Blink
	case settingOpenAIKey:
		m.mode = modeEditOpenAIKey
		m.textInput.SetValue(m.aiKeys.OpenAI)
		m.textInput.EchoMode = textinput.EchoPassword
		m.textInput.Placeholder = tr("OpenAI API key — also needed to transcribe recordings (clear to remove)")
		m.textInput.Focus()
		return m, textinput.Blink
	case settingGeminiKey:
		m.mode = modeEditGeminiKey
		m.textInput.SetValue(m.aiKeys.Gemini)
		m.textInput.EchoMode = textinput.EchoPassword
		m.textInput.Placeholder = tr("Gemini API key (clear the field to remove it)")
		m.textInput.Focus()
		return m, textinput.Blink
	case settingMistralKey:
		m.mode = modeEditMistralKey
		m.textInput.SetValue(m.aiKeys.Mistral)
		m.textInput.EchoMode = textinput.EchoPassword
		m.textInput.Placeholder = tr("Mistral API key (clear the field to remove it)")
		m.textInput.Focus()
		return m, textinput.Blink
	case settingFFmpegInput:
		m.mode = modeEditFFmpegInput
		m.textInput.SetValue(m.ffmpegInput)
		m.textInput.Placeholder = tr("Microphone override: format:input, e.g. dshow:audio=Microphone")
		m.textInput.Focus()
		return m, textinput.Blink
	case settingWhisperBinOverride:
		m.mode = modeEditWhisperBinOverride
		m.textInput.SetValue(m.whisperBinOverride)
		m.textInput.Placeholder = tr("whisper-cli path override (leave blank to use PATH)")
		m.textInput.Focus()
		return m, textinput.Blink
	case settingWhisperLanguage:
		m.mode = modeEditWhisperLanguage
		m.textInput.SetValue(m.whisperLanguage)
		m.textInput.Placeholder = tr("Language code, e.g. da (blank auto-detects)")
		m.textInput.Focus()
		return m, textinput.Blink
	case settingWhisperModelStatus:
		return m, m.promptDownloadWhisperModel()
	default:
		// Every remaining row is a toggle or a picker, and enter means the
		// same on it as →. One table, so a row cannot answer one key and not
		// the other — which is exactly what the two hand-kept chains did.
		return m, m.settingsAdjust(+1)
	}
}

// settingsAdjust applies a value change to the selected Settings row: dir is
// +1 for →/enter and -1 for ←. Toggles ignore dir; the pickers cycle by it.
// It returns a command because one row can open a modal: switching the server
// on without a token asks for the token rather than refusing.
func (m *model) settingsAdjust(dir int) tea.Cmd {
	if m.isBiasSettingRow(m.settingsCursor) {
		m.cycleBias(m.settingsCursor, dir)
		return nil
	}
	switch m.settingsCursor {
	case settingAging:
		m.toggleAging()
	case settingAutoCloseParent:
		m.toggleAutoCloseParent()
	case settingAutoCloseSubtasks:
		m.toggleAutoCloseSubtasks()
	case settingShowBoard:
		m.toggleShowBoard()
	case settingTheme:
		m.cycleTheme(dir)
	case settingLanguage:
		m.cycleLang(dir)
	case settingDetailPos:
		m.cycleDetailPos(dir)
	case settingReminder:
		m.toggleReminder()
	case settingReminderTime:
		m.cycleReminder(dir)
	case settingSubtaskTags:
		m.subtaskTags = !m.subtaskTags
		m.persistSettings()
	case settingSyncAuto:
		m.toggleSyncAuto()
	case settingSyncBoard:
		m.toggleSyncBoard()
	case settingServerOn:
		return m.startStopServer()
	case settingAIProvider:
		m.cycleAIProvider(dir)
	case settingUseLocalWhisper:
		return m.toggleUseLocalWhisper()
	}
	return nil
}

// toggleUseLocalWhisper is the local-Whisper row's ←/→/enter. Turning it
// off never deletes the model — startStopServer's "stopping is just the
// toggle" has no equivalent reason here to throw away a ~574MB download
// over a flip. Turning it on when the model isn't present yet opens the
// same y/n confirm every other system-affecting action in this app uses
// before running, the same way startStopServer defers to a token editor
// instead of flipping the bool outright when starting needs one first.
func (m *model) toggleUseLocalWhisper() tea.Cmd {
	if m.useLocalWhisper {
		m.useLocalWhisper = false
		m.persistSettings()
		return nil
	}
	if _, ok := resolveWhisperBinPath(m.whisperBinOverride); !ok {
		return m.promptInstallWhisperCLI()
	}
	if present, _ := whisperModelStatus(); present {
		m.useLocalWhisper = true
		m.persistSettings()
		return nil
	}
	return m.promptDownloadWhisperModel()
}

// promptInstallWhisperCLI is toggleUseLocalWhisper's own detour when
// whisper-cli isn't found yet — the same y/n-before-any-system-modifying-
// action gate promptInstallFFmpeg (update_meetings.go) uses, just resumed
// into continuing the on/off toggle (confirmInstallWhisperCLI) rather than
// a specific recording afterward. ok is false when no installer this
// knows how to drive is on PATH (see whisperCLIInstallCommand), in which
// case this falls back to the same copy-paste hint whisper-cli-not-found
// shows everywhere else.
func (m *model) promptInstallWhisperCLI() tea.Cmd {
	name, args, ok := whisperCLIInstallCommand()
	if !ok {
		m.flashError(fmt.Sprintf(tr("whisper-cli not found — %s"), whisperCLIInstallHint()))
		return clearErrAfter()
	}
	m.mode = modeConfirm
	m.confirmMsg = fmt.Sprintf(tr("whisper-cli isn't installed. Run '%s' now? (y/n)"),
		strings.Join(append([]string{name}, args...), " "))
	m.confirmOnYes = (*model).confirmInstallWhisperCLI
	return nil
}

// confirmInstallWhisperCLI is modeConfirm's y/n action for
// promptInstallWhisperCLI — it re-resolves the command rather than
// threading it through confirmMsg, so what runs is always freshly checked
// against PATH, not a stale string, the same reasoning
// confirmInstallFFmpeg follows.
func (m *model) confirmInstallWhisperCLI() tea.Cmd {
	name, args, ok := whisperCLIInstallCommand()
	if !ok {
		m.flashError(fmt.Sprintf(tr("whisper-cli not found — %s"), whisperCLIInstallHint()))
		return clearErrAfter()
	}
	return whisperCLIInstallCmd(name, args)
}

// handleWhisperCLIInstallFinished resumes toggleUseLocalWhisper's own
// on-flow once whisper-cli is confirmed to actually be on PATH — exit 0
// doesn't guarantee it landed (a declined sudo prompt can still exit
// clean), so this re-checks rather than trusting the exit code alone, the
// same reasoning handleFFmpegInstallFinished uses. On success it continues
// exactly where toggleUseLocalWhisper would have: straight on if the model
// is already there, or into the same download confirm if not.
func (m model) handleWhisperCLIInstallFinished(msg whisperCLIInstallFinishedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.flashError(fmt.Sprintf(tr("whisper-cli install failed: %v"), msg.err))
		return m, clearErrAfter()
	}
	if _, ok := resolveWhisperBinPath(m.whisperBinOverride); !ok {
		m.flashError(tr("whisper-cli still isn't on PATH after install — see the output above"))
		return m, clearErrAfter()
	}
	if present, _ := whisperModelStatus(); present {
		m.useLocalWhisper = true
		m.persistSettings()
		m.flashSuccess(tr("whisper-cli installed"))
		return m, clearErrAfter()
	}
	return m, m.promptDownloadWhisperModel()
}

// promptDownloadWhisperModel is shared by the on/off toggle (turning it on
// with no model yet) and settingWhisperModelStatus's own enter (fetching
// ahead of time, or re-fetching after a corrupt/partial download) — same
// confirm, same download either way. handleWhisperModelDownloadDone turns
// useLocalWhisper on for both entry points on a successful download:
// downloading it, however that got started, is always "I want to use
// this now."
func (m *model) promptDownloadWhisperModel() tea.Cmd {
	m.mode = modeConfirm
	m.confirmMsg = fmt.Sprintf(tr("Download the local Whisper model (~%s) now? (y/n)"), humanBytes(whisperModelSizeBytes))
	m.confirmOnYes = func(m *model) tea.Cmd {
		m.flashInfo(tr("Downloading model… this can take a few minutes"))
		return downloadWhisperModelCmd()
	}
	return nil
}

// handleWhisperModelDownloadDone lands downloadWhisperModelCmd's result —
// see promptDownloadWhisperModel's doc comment for why success always
// turns useLocalWhisper on, regardless of which row's enter started it.
func (m model) handleWhisperModelDownloadDone(msg whisperModelDownloadDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.flashError(fmt.Sprintf(tr("Whisper model download failed: %v"), msg.err))
		return m, clearErrAfter()
	}
	m.useLocalWhisper = true
	m.persistSettings()
	m.flashSuccess(tr("Local Whisper model downloaded"))
	return m, clearErrAfter()
}

// cycleDetailPos moves the detail pane to the next placement. The rendered
// detail is cached against the width it was drawn at, and the two layouts draw
// it at very different widths, so the cache goes with the move.
func (m *model) cycleDetailPos(dir int) {
	m.detailPos = nextDetailPos(m.detailPos, dir)
	m.invalidateDetailCache()
	m.persistSettings()
}

// startStopServer is the Server row's ←/→/enter. Stopping is just the toggle;
// starting needs a token, and the row that holds it is hidden while the server
// is off, so the first start opens the token editor and picks the start back up
// when it is saved. Refusing with "set a token first" would point at a row that
// is not on screen.
func (m *model) startStopServer() tea.Cmd {
	if m.inprocServer == nil && m.syncCfg.ServerToken == "" {
		m.serverStartAfterToken = true
		m.mode = modeEditServerToken
		m.textInput.SetValue("")
		m.textInput.EchoMode = textinput.EchoPassword
		m.textInput.Placeholder = tr("Server token clients must present (ctrl+g generates one · blank removes it)")
		m.textInput.Focus()
		return textinput.Blink
	}
	m.toggleServer()
	return nil
}

// updateConfirmUpdate handles the "newer release available — update now?" prompt.
func (m model) updateConfirmUpdate(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "y", "enter":
			m.mode = modeNormal
			m.updateStatus = tr("Updating…")
			return m, func() tea.Msg {
				return updateDoneMsg{err: selfUpdate()}
			}
		case "n", "esc":
			m.mode = modeNormal
		}
	}
	return m, nil
}

// checkForUpdate queries the latest release tag asynchronously.
func checkForUpdate() tea.Cmd {
	return func() tea.Msg {
		latest, err := latestRelease()
		return updateCheckMsg{latest: latest, err: err}
	}
}

// cycleTheme advances the theme by dir (+1 / -1), applies it, and persists.
func (m *model) cycleTheme(dir int) {
	idx := 0
	for i, t := range themes {
		if t.name == m.themeName {
			idx = i
			break
		}
	}
	idx = (idx + dir + len(themes)) % len(themes)
	m.themeName = themes[idx].name
	applyTheme(themes[idx])
	m.persistSettings()
	m.invalidateDetailCache()
	m.markCacheDirty()
}

// cycleLang steps the UI language through availableLanguages. Only what the user
// sees changes; stored data (titles, tags, dates) is untouched.
func (m *model) cycleLang(dir int) {
	idx := 0
	for i, l := range availableLanguages {
		if l == activeLang {
			idx = i
			break
		}
	}
	idx = (idx + dir + len(availableLanguages)) % len(availableLanguages)
	applyLang(string(availableLanguages[idx]))
	m.applyLangPlaceholders()
	m.persistSettings()
	m.invalidateDetailCache()
	m.markCacheDirty()
}

// applyLangPlaceholders sets every text-input placeholder from the active
// language. Called at startup and whenever the language changes so the prompts
// switch live rather than waiting for a restart.
func (m *model) applyLangPlaceholders() {
	m.searchInput.Placeholder = searchHint()
	m.depSearchInput.Placeholder = tr("Search for task to add as dependency...")
	m.tagSearchInput.Placeholder = tr("Search or create tag...")
	m.projSearchInput.Placeholder = tr("Search or create project...")
	m.tagTabSearchInput.Placeholder = tr("Filter tags...")
}

func (m model) handleListRename() (tea.Model, tea.Cmd) {
	switch m.tab {
	case tabCalendar:
		if m.calendar.focusTimeline {
			acts := m.activitiesForDay(m.calendar.selected)
			if m.calendar.entryCursor < len(acts) {
				a := acts[m.calendar.entryCursor]
				return m, m.startEditTimeEntry(a.taskID, a.entryID)
			}
		}
	case tabTags:
		if m.tagTaskMode {
			return m.startEditTaskTitle()
		}
		if tags := m.getFilteredTagsForTab(); m.tagTabCursor < len(tags) && tags[m.tagTabCursor] != untaggedKey {
			m.editingTagName = tags[m.tagTabCursor]
			m.mode = modeEditTag
			m.textInput.SetValue(tags[m.tagTabCursor])
			m.textInput.Placeholder = tr("Edit tag name...")
			m.textInput.Focus()
			return m, textinput.Blink
		}
	case tabTasks:
		if !m.showHistory {
			return m.startEditTaskTitle()
		}
	case tabProjects:
		if m.projectTaskMode {
			return m.startEditTaskTitle()
		}
		{
			if projects := m.allProjectsForList(); m.projectCursor < len(projects) {
				m.editingProjectName = projects[m.projectCursor]
				m.mode = modeEditProjectInline
				m.textInput.SetValue(projects[m.projectCursor])
				m.textInput.Focus()
				return m, textinput.Blink
			}
		}
	}
	return m, nil
}

func (m model) handleListDelete() (tea.Model, tea.Cmd) {
	switch m.tab {
	case tabCalendar:
		if m.calendar.focusTimeline {
			acts := m.activitiesForDay(m.calendar.selected)
			if m.calendar.entryCursor < len(acts) {
				a := acts[m.calendar.entryCursor]
				m.mode = modeConfirm
				m.confirmOnYes = (*model).confirmDeleteTimeEntry
				m.pendingEntryTaskID = a.taskID
				m.pendingEntryID = a.entryID
				m.confirmMsg = fmt.Sprintf(tr("Delete %s entry for '%s'? (y/n)"),
					formatDuration(a.duration()), truncate(a.title, 30))
			}
		}
	case tabTags:
		if m.tagTaskMode {
			return m.stageDeleteTask()
		}
		if tags := m.getFilteredTagsForTab(); m.tagTabCursor < len(tags) && tags[m.tagTabCursor] != untaggedKey {
			m.mode = modeConfirm
			m.confirmOnYes = (*model).confirmDeleteTagGlobal
			m.confirmMsg = fmt.Sprintf(tr("Delete tag '#%s' from ALL tasks? (y/n)"), tags[m.tagTabCursor])
		}
	case tabProjects:
		if m.projectTaskMode {
			return m.stageDeleteTask()
		}
		// The help has always advertised x as "delete globally" here, but
		// nothing was wired to it. Clearing the project off its tasks is the
		// mirror of the r rename, and leaves the tasks themselves alone.
		if projects := m.allProjectsForList(); m.projectCursor < len(projects) {
			m.pendingProjectName = projects[m.projectCursor]
			m.mode = modeConfirm
			m.confirmOnYes = (*model).confirmDeleteProjectGlobal
			m.confirmMsg = fmt.Sprintf(tr("Remove project '%s' from ALL its tasks? (y/n)"), projects[m.projectCursor])
		}
	case tabTasks:
		return m.stageDeleteTask()
	}
	return m, nil
}
