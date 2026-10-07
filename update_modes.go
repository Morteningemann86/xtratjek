package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Morteningemann86/xtratjek/rank"
	"github.com/Morteningemann86/xtratjek/todo"
	tea "github.com/charmbracelet/bubbletea"
)

// ── Input handlers ────────────────────────────────────────────────────────────

func (m model) updateInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			val := m.textInput.Value()
			if m.pane == paneList && strings.TrimSpace(val) != "" && parseQuickAdd(val).title == "" {
				// Only tokens, nothing to call the task: keep the field open so
				// the title can go in front of them.
				m.flashError(tr("A task needs a title"))
				return m, clearErrAfter()
			}
			m.mode = modeNormal
			if m.pane == paneList {
				if val != "" {
					parsed := parseQuickAdd(val)
					t := todo.New(parsed.title)
					t.Priority = parsed.priority
					t.Size = parsed.size
					if !parsed.dueDate.IsZero() {
						t.DueDate = parsed.dueDate
					}
					if parsed.project != "" {
						t.Project = parsed.project
					}
					for _, tg := range parsed.tags {
						t.AddTag(tg)
					}
					if parsed.recurrence != "" {
						t.Recurrence = parsed.recurrence
					}
					// Resolve dep: refs before the add so `^` still points at
					// the previous task, not the one being created. A bad ref
					// doesn't block the add — the task lands, the toast says
					// which link didn't.
					var depErr error
					for _, ref := range parsed.deps {
						dep, err := resolveDepRef(m.allTodos(), ref)
						if err != nil {
							depErr = err
							continue
						}
						t.AddDependency(dep.ID)
					}
					// Added from the Board: the card lands in the column you were
					// looking at. Done is not a place to create work, so from
					// there it lands in the first column.
					onBoard := m.tab == tabBoard
					if onBoard {
						if col := m.board.addCol; col > 0 && col < m.boardCfg.doneColumn() {
							t.SetStage(m.boardCfg.pending()[col])
						}
					}
					m.pushUndo("add task", t.ID)
					m.add(t)
					m.markModified(t.ID)
					saveLastAddedID(t.ID)
					if onBoard {
						m.boardFollow(m.boardCfg.stageIndex(t.Stage), t.ID)
						if depErr != nil {
							m.flashError(fmt.Sprintf("%s: %v", tr("Dependency not linked"), depErr))
							return m, clearErrAfter()
						}
						return m, nil
					}
					// Position the cursor on the newly added task and open its
					// detail view so the user lands on it immediately. A live
					// search/focus filter can hide the new task — then the
					// cursor can't follow it, and opening the detail pane
					// would show whatever task the cursor sits on, so only
					// switch panes when the cursor actually reached it.
					m.followTask(t.ID)
					if cur := m.currentTodo(); cur != nil && cur.ID == t.ID {
						m.detailTaskID = t.ID
						m.detailStack = nil
						m.pane = paneDetail
						m.detail = detailState{field: fieldStartDate}
						m.invalidateDetailCache()
						m.pushFocus(stateDetailPane)
					}
					if depErr != nil {
						m.flashError(fmt.Sprintf("%s: %v", tr("Dependency not linked"), depErr))
						return m, clearErrAfter()
					}
					if len(parsed.unparsed) > 0 {
						m.flashError(fmt.Sprintf(tr("Kept in the title, not understood: %s"), strings.Join(parsed.unparsed, " ")))
						return m, clearErrAfter()
					}
				}
			} else if t := m.currentTodo(); t != nil {
				if m.detail.field == fieldComments {
					if val != "" {
						m.pushUndo("add comment", t.ID)
						t.AddComment(val)
						m.detail.commentCursor = len(t.Comments) - 1
						m.markModified(t.ID)
					}
				} else {
					switch m.detail.field {
					case fieldStartDate:
						if val == "" {
							m.pushUndo("clear start date", t.ID)
							t.StartDate = time.Time{}
						} else if d, err := parseDueDate(val); err == nil {
							m.pushUndo("set start date", t.ID)
							t.SetStartDate(d)
						} else {
							m.flashError(invalidDateMsg())
							return m, clearErrAfter()
						}
					case fieldCompleted:
						if !completedFieldVisible(t) {
							break
						}
						d, err := parseCompletedAt(val, t.CompletedAt, time.Now())
						if err != nil {
							m.flashError(err.Error())
							return m, clearErrAfter()
						}
						m.pushUndo("set completion date", t.ID)
						t.SetCompletedAt(d)
					case fieldDueDate:
						if val == "" {
							// A parent deadline applies to its whole subtree, so capture
							// every affected task for undo before clearing descendants too.
							if m.subtaskCount(t.ID) > 0 {
								m.pushUndo("clear due date")
							} else {
								m.pushUndo("clear due date", t.ID)
							}
							t.SetDueDate(time.Time{})
						} else if d, err := parseDueDate(val); err == nil {
							// A subtask may extend ancestors and a parent updates all
							// descendants, so capture the full tree when either applies.
							if t.ParentID != "" || m.subtaskCount(t.ID) > 0 {
								m.pushUndo("set due date")
							} else {
								m.pushUndo("set due date", t.ID)
							}
							t.SetDueDate(d)
						} else {
							m.flashError(invalidDateMsg())
							return m, clearErrAfter()
						}
					}
					ids := []string{t.ID}
					if m.detail.field == fieldDueDate {
						ids = append(ids, m.propagateDueToSubtasks(t.ID)...)
						if t.ParentID != "" {
							ids = append(ids, m.extendParentDueIfNeeded(t.ID)...)
						}
					}
					m.markModified(ids...)
				}
			}
			return m, nil
		case "esc":
			m.mode = modeNormal
			return m, nil
		case "ctrl+e":
			// Escape hatch only for the comment-add input (the comments page);
			// other modeInput uses (quick-add, date fields) keep ctrl+e as the
			// text input's move-to-end.
			if m.pane != paneList && m.detail.field == fieldComments {
				return m, m.openEditorForInput()
			}
		case "tab", "up", "down":
			if _, matches := m.completionMatches(); m.applyCompletionKey(key.String(), &m.textInput, matches) {
				return m, nil
			}
		}
	}
	before, beforePos := m.textInput.Value(), m.textInput.Position()
	m.textInput, cmd = m.textInput.Update(msg)
	if m.textInput.Value() != before || m.textInput.Position() != beforePos {
		m.suggestCursor = 0 // the token changed — re-aim at the best match
	}
	return m, cmd
}

func (m model) updateAddSubtask(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			if val := strings.TrimSpace(m.textInput.Value()); val != "" {
				if t := m.currentTodo(); t != nil {
					// Build the subtask, push undo with its (not-yet-stored) ID
					// so undo will delete it (the ID is in entry.ids but has no
					// captured partial), then add to the store.
					sub := todo.NewSubtask(val, t.ID)
					sub.InheritContextFrom(m.get(t.ID), m.subtaskTags)
					m.pushUndo("add subtask", sub.ID)
					m.add(sub)
					m.detail.subtaskCursor = m.subtaskCount(t.ID) - 1
					m.markModified(sub.ID)
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

// updateAddTimeEntry parses a duration ("45m", "1h30m") or a clock range
// ("10:00-10:30") and appends a TimeEntry to pendingEntryTaskID. Duration
// form anchors the entry at "now"; range form anchors it on today. Letting
// a user log time after the fact is useful for tracking work that wasn't
// captured by the live timer — and for back-dating, the entry can later be
// retimed via the existing modeEditTimeEntry flow.
func (m model) updateAddTimeEntry(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			t := m.findTodoByID(m.pendingEntryTaskID)
			if t == nil {
				m.mode = modeNormal
				return m, nil
			}
			start, stop, err := parseManualEntry(m.textInput.Value(), time.Now())
			if err != nil {
				m.flashError(err.Error())
				return m, clearErrAfter()
			}
			m.pushUndo("add time entry", t.ID)
			t.AddTimeEntry(start, stop)
			m.markModified(t.ID)
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

// updateEditSubtask renames the subtask whose index was captured in
// pendingSubtask when edit started. The index is captured up front so
// concurrent reorders/deletions can't accidentally rename a different child.
func (m model) updateEditSubtask(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			if val := strings.TrimSpace(m.textInput.Value()); val != "" {
				if t := m.currentTodo(); t != nil {
					ids := m.subtaskIDs(t.ID)
					if m.pendingSubtask < len(ids) {
						if sub := m.findTodoByID(ids[m.pendingSubtask]); sub != nil {
							m.pushUndo("rename subtask", sub.ID)
							sub.Title = todo.CapitalizeTitle(val)
							sub.ModifiedAt = todo.StampModified(sub.ModifiedAt)
							m.markModified(sub.ID)
						}
					}
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

// updateEditDue applies a due date typed from the task list. An empty value
// clears the date, which is how the detail-pane field behaves too; an
// unparseable one leaves the task alone and says so rather than silently
// dropping the edit.
func (m model) updateEditDue(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			val := strings.TrimSpace(m.textInput.Value())
			t := m.currentTodo()
			if t == nil {
				m.mode = modeNormal
				return m, nil
			}
			if val == "" {
				if !t.DueDate.IsZero() {
					m.pushUndo("clear due date", t.ID)
					t.DueDate = time.Time{}
					t.ModifiedAt = todo.StampModified(t.ModifiedAt)
					m.markModified(t.ID)
				}
				m.mode = modeNormal
				return m, nil
			}
			d, err := parseDueDate(val)
			if err != nil {
				// Stay in the prompt: the typed text is still there to fix.
				m.flashError(invalidDateMsg())
				return m, clearErrAfter()
			}
			m.pushUndo("set due date", t.ID)
			t.SetDueDate(d)
			m.markModified(t.ID)
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

func (m model) updateEditTitle(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			if newTitle := strings.TrimSpace(m.textInput.Value()); newTitle != "" {
				if t := m.currentTodo(); t != nil {
					m.pushUndo("rename task", t.ID)
					t.Title = todo.CapitalizeTitle(newTitle)
					t.ModifiedAt = todo.StampModified(t.ModifiedAt)
					m.markModified(t.ID)
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

func (m model) updateEditComment(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			if t := m.currentTodo(); t != nil {
				if val := m.textInput.Value(); val != "" {
					m.pushUndo("edit comment", t.ID)
					t.UpdateComment(m.pendingComment, val)
					m.markModified(t.ID)
				}
			}
			m.mode = modeNormal
			return m, nil
		case "esc":
			m.mode = modeNormal
			return m, nil
		case "ctrl+e":
			return m, m.openEditorForInput()
		}
	}
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

func (m model) updateEditTag(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			if newName := strings.TrimSpace(m.textInput.Value()); newName != "" && newName != m.editingTagName {
				// renameTagGlobally walks every task; the affected IDs aren't
				// known until after the walk, so capture full pre-state.
				m.pushUndo("rename tag")
				touched := m.renameTagGlobally(m.editingTagName, newName)
				m.markModified(touched...)
			}
			m.mode = modeNormal
			m.editingTagName = ""
			return m, nil
		case "esc":
			m.mode = modeNormal
			m.editingTagName = ""
			return m, nil
		}
	}
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

func (m model) updateEditProjectInline(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			if newName := strings.TrimSpace(m.textInput.Value()); newName != "" && newName != m.editingProjectName {
				m.pushUndo("rename project")
				touched := m.renameProjectGlobally(m.editingProjectName, newName)
				m.markModified(touched...)
			}
			m.mode = modeNormal
			m.editingProjectName = ""
			return m, nil
		case "esc":
			m.mode = modeNormal
			m.editingProjectName = ""
			return m, nil
		}
	}
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

// ── Search handlers ───────────────────────────────────────────────────────────

func (m model) updateSearch(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			m.mode = modeNormal
			m.searchQuery = m.searchInput.Value()
			if m.searchQuery != "" {
				m.pushFocus(stateSearch)
			} else {
				m.dropFocus(stateSearch)
			}
			m.cursor = 0
			m.projectCursor = 0
			m.listOffset = 0
			m.markFilterDirty()
			m.persistSettings()
			return m, nil
		case "esc":
			// Cancel: discard the query and restore the unfiltered list.
			m.mode = modeNormal
			m.searchInput.SetValue("")
			m.searchQuery = ""
			m.cursor = 0
			m.projectCursor = 0
			m.listOffset = 0
			m.markFilterDirty()
			m.persistSettings()
			return m, nil
		case "tab", "up", "down":
			// Completions, on the tabs whose search runs the token grammar.
			// Enter keeps applying the filter, exactly as in quick-add.
			if _, matches := m.completionMatches(); m.applyCompletionKey(key.String(), &m.searchInput, matches) {
				m.searchQuery = m.searchInput.Value()
				m.markFilterDirty()
				return m, nil
			}
		}
	}
	before, beforePos := m.searchInput.Value(), m.searchInput.Position()
	m.searchInput, cmd = m.searchInput.Update(msg)
	if m.searchInput.Value() != before || m.searchInput.Position() != beforePos {
		m.suggestCursor = 0 // the token changed — re-aim at the best match
	}
	newQuery := m.searchInput.Value()
	if newQuery != m.searchQuery {
		// Only invalidate caches and reset cursor when the query
		// actually changed. Otherwise cursor-blink ticks would
		// rebuild caches on every frame, reshuffling tied done
		// tasks (see rank.SortValues tiebreakers).
		m.searchQuery = newQuery
		m.cursor = 0
		m.projectCursor = 0
		m.listOffset = 0
		m.markFilterDirty()
	}
	return m, cmd
}

func (m model) updateSearchTagTab(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			m.mode = modeNormal
			m.tagTabCursor = 0
			if m.tagTabSearchQuery != "" {
				m.pushFocus(stateTagSearch)
			} else {
				m.dropFocus(stateTagSearch)
			}
			return m, nil
		case "esc":
			// Cancel: discard the filter and restore the full tag list.
			m.mode = modeNormal
			m.tagTabSearchInput.SetValue("")
			m.tagTabSearchQuery = ""
			m.tagTabCursor = 0
			return m, nil
		}
	}
	m.tagTabSearchInput, cmd = m.tagTabSearchInput.Update(msg)
	m.tagTabSearchQuery = m.tagTabSearchInput.Value()
	m.tagTabCursor = 0
	return m, cmd
}

func (m model) updateSearchDep(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			results := m.depSearchResults()
			if m.depSearch.cursor < len(results) {
				if t := m.currentTodo(); t != nil {
					m.pushUndo("add dependency", t.ID)
					t.AddDependency(results[m.depSearch.cursor].ID)
					m.markModified(t.ID)
				}
			}
			m.mode = modeNormal
			m.depSearch = searchState{}
			return m, nil
		case "up":
			if m.depSearch.cursor > 0 {
				m.depSearch.cursor--
			}
			return m, nil
		case "down":
			if results := m.depSearchResults(); m.depSearch.cursor < len(results)-1 {
				m.depSearch.cursor++
			}
			return m, nil
		case "esc":
			m.mode = modeNormal
			m.depSearch = searchState{}
			return m, nil
		}
	}
	oldQuery := m.depSearch.query
	m.depSearchInput, cmd = m.depSearchInput.Update(msg)
	m.depSearch.query = m.depSearchInput.Value()
	if m.depSearch.query != oldQuery {
		m.depSearch.cursor = 0
	}
	return m, cmd
}

func (m model) updateSearchTag(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			if t := m.currentTodo(); t != nil {
				results := m.tagSearchResults()
				var tagToAdd string
				if m.tagSearch.cursor < len(results) {
					tagToAdd = results[m.tagSearch.cursor]
				} else if m.tagSearch.query != "" {
					tagToAdd = m.tagSearch.query
				}
				if tagToAdd != "" {
					m.pushUndo("add tag", t.ID)
					t.AddTag(tagToAdd)
					m.markModified(t.ID)
				}
			}
			m.mode = modeNormal
			m.tagSearch = searchState{}
			return m, nil
		case "up":
			if m.tagSearch.cursor > 0 {
				m.tagSearch.cursor--
			}
			return m, nil
		case "down":
			if results := m.tagSearchResults(); m.tagSearch.cursor < len(results)-1 {
				m.tagSearch.cursor++
			}
			return m, nil
		case "esc":
			m.mode = modeNormal
			m.tagSearch = searchState{}
			return m, nil
		}
	}
	oldQuery := m.tagSearch.query
	m.tagSearchInput, cmd = m.tagSearchInput.Update(msg)
	m.tagSearch.query = m.tagSearchInput.Value()
	if m.tagSearch.query != oldQuery {
		m.tagSearch.cursor = 0
	}
	return m, cmd
}

func (m model) updateSearchProject(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			if t := m.currentTodo(); t != nil {
				results := m.projSearchResults()
				var projToSet string
				if m.projSearch.cursor < len(results) {
					projToSet = results[m.projSearch.cursor]
				} else if m.projSearch.query != "" {
					projToSet = m.projSearch.query
				}
				if projToSet != "" {
					m.setProject(t, projToSet, "set project")
				}
			}
			m.mode = modeNormal
			m.projSearch = searchState{}
			return m, nil
		case "up":
			if m.projSearch.cursor > 0 {
				m.projSearch.cursor--
			}
			return m, nil
		case "down":
			if results := m.projSearchResults(); m.projSearch.cursor < len(results)-1 {
				m.projSearch.cursor++
			}
			return m, nil
		case "esc":
			m.mode = modeNormal
			m.projSearch = searchState{}
			return m, nil
		}
	}
	oldQuery := m.projSearch.query
	m.projSearchInput, cmd = m.projSearchInput.Update(msg)
	m.projSearch.query = m.projSearchInput.Value()
	if m.projSearch.query != oldQuery {
		m.projSearch.cursor = 0
	}
	return m, cmd
}

// ── Confirm-delete handlers ───────────────────────────────────────────────────

// updateConfirm is the single handler for every modeConfirm prompt: y/enter runs
// the staged confirmOnYes action, n/esc cancels, and either way the prompt
// closes. The per-action bodies live in the confirm* methods below, staged at
// each trigger site — so prompt behavior (Enter-as-yes, cancel) is uniform by
// construction.
func (m model) updateConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "y", "enter":
			var cmd tea.Cmd
			if m.confirmOnYes != nil {
				cmd = m.confirmOnYes(&m)
			}
			m.mode = modeNormal
			m.confirmOnYes = nil
			return m, cmd
		case "n", "esc":
			m.mode = modeNormal
			m.confirmOnYes = nil
		}
	}
	return m, nil
}

func (m *model) confirmDeleteTask() tea.Cmd {
	if id := m.pendingDeleteID; id != "" && m.get(id) != nil {
		ids := m.descendantIDs(id)
		m.pushUndo("delete task", ids...)
		for _, deleteID := range ids {
			m.markTombstone(deleteID)
			m.remove(deleteID)
		}
	}
	m.pendingDeleteID = ""
	// Tombstone already records what to persist; no other rows changed, so we
	// only need to schedule a save and refresh derived caches.
	m.dirty = true
	m.cache.dirty = true
	m.invalidateDetailCache()
	m.refreshCaches()
	var newLen int
	if m.showHistory {
		newLen = len(m.cache.done)
	} else {
		newLen = m.visibleActiveLen()
	}
	if m.cursor >= newLen && m.cursor > 0 {
		m.cursor--
	}
	return nil
}

// confirmReopen backs the "Move to active?" prompt staged by the Tasks-tab 'd'
// handler when the cursor is on a done task: it reopens the task (voiding the
// completion-rank reading via Toggle).
func (m *model) confirmReopen() tea.Cmd {
	if id := m.pendingReopenID; id != "" {
		if t := m.get(id); t != nil && t.Status == todo.Done {
			m.pushUndo("reopen task", t.ID)
			t.Toggle()
			m.markModified(t.ID)
			// The row leaves the done/history list, so the cursor would land on
			// the next row — decrement so it lands on the previous one instead.
			// Subtasks stay visible. Only the Tasks tab owns m.cursor; a reopen
			// confirmed from the Board must not nudge it.
			if m.tab == tabTasks && t.ParentID == "" && m.cursor > 0 {
				m.cursor--
			}
			// A board-staged reopen follows the card back to its stage column.
			if m.tab == tabBoard {
				m.boardFollow(m.boardCfg.stageIndex(t.Stage), t.ID)
			}
		}
	}
	m.pendingReopenID = ""
	return nil
}

// confirmCloseParent backs the "close parent with open subtasks?" prompt staged
// by the Tasks-tab 'd' handler: it closes the parent (and spawns next recurrence
// if the parent was recurring) but does NOT touch the open subtasks — the user
// opted to close just the parent, not cascade.
func (m *model) confirmCloseParent() tea.Cmd {
	if id := m.pendingCloseParentID; id != "" {
		if t := m.get(id); t != nil && t.Status == todo.Pending {
			// Full snapshot: spawnNextRecurrence creates a new task, and undo
			// must remove it plus restore t. Capturing all state is simpler than
			// tracking the new ID separately.
			m.pushUndo("close task")
			if t.IsTimerRunning() {
				m.stopTimer(t.ID)
			}
			rank.CaptureRankAtDone(m.rank, m.allTodos(), t)
			t.Toggle()
			ids := []string{t.ID}
			if t.IsRecurring() {
				if newID := m.spawnNextRecurrence(t); newID != "" {
					ids = append(ids, newID)
				}
			}
			m.markModified(ids...)
			if m.cursor > 0 {
				m.cursor--
			}
		}
	}
	m.pendingCloseParentID = ""
	return nil
}

func (m *model) confirmDeleteComment() tea.Cmd {
	if t := m.currentTodo(); t != nil {
		m.pushUndo("delete comment", t.ID)
		t.DeleteComment(m.pendingComment)
		if m.detail.commentCursor >= len(t.Comments) && m.detail.commentCursor > 0 {
			m.detail.commentCursor--
		}
		m.markModified(t.ID)
	}
	return nil
}

func (m *model) confirmDeleteDep() tea.Cmd {
	if t := m.currentTodo(); t != nil && m.pendingDep < len(t.Dependencies) {
		m.pushUndo("remove dependency", t.ID)
		t.RemoveDependency(t.Dependencies[m.pendingDep])
		if m.detail.depCursor >= len(t.Dependencies) && m.detail.depCursor > 0 {
			m.detail.depCursor--
		}
		m.markModified(t.ID)
	}
	return nil
}

func (m model) updateEditTimeEntry(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			t := m.findTodoByID(m.pendingEntryTaskID)
			if t == nil {
				m.mode = modeNormal
				return m, nil
			}
			for i := range t.TimeEntries {
				if t.TimeEntries[i].ID == m.pendingEntryID {
					e := &t.TimeEntries[i]
					start, stop, err := parseEntryEdit(m.textInput.Value(), e.StartedAt, e.IsRunning())
					if err != nil {
						m.flashError(err.Error())
						return m, clearErrAfter()
					}
					m.pushUndo("edit time entry", t.ID)
					e.StartedAt = start
					e.StoppedAt = stop
					e.ModifiedAt = todo.StampModified(e.ModifiedAt)
					t.ModifiedAt = todo.StampModified(t.ModifiedAt)
					m.markModified(t.ID)
					break
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

// updateEditStages handles the Settings-tab editor for the kanban stage list:
// one comma-separated line of column names. Applying it re-renders the Board
// immediately and persists to settings.json "stages", so the columns are no
// longer something you have to leave the app to rename.
func (m model) updateEditStages(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			stages, icons, doneIcon, err := parseStagesInput(m.textInput.Value())
			if err != nil {
				// Keep the field open with the text as typed, so the one
				// icon is fixed rather than the whole line retyped.
				msg := fmt.Sprintf(tr("An icon is one character wide; %s is not"), err)
				if errors.Is(err, errDoneIconTaken) {
					msg = tr("✓ is kept for the last column; pick another icon")
				}
				m.flashError(msg)
				return m, clearErrAfter()
			}
			m.applyStageEdit(stages, icons)
			m.mode = modeNormal
			if doneIcon != "" {
				m.flashInfo(fmt.Sprintf(tr("The last column keeps ✓; its icon %s was left out"), doneIcon))
				return m, clearErrAfter()
			}
			return m, nil
		case "esc":
			m.mode = modeNormal
			return m, nil
		}
	}
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

// applyStageEdit swaps the active stage list for next, carries the cards of
// any renamed/dropped stage over to their new column (stageRemap), persists
// the list, and invalidates the board column cache. A no-op edit changes
// nothing — no task touched, no save.
//
// Deliberately not undoable, like the other settings (theme, language, the
// bias knobs): the undo stack holds task state only, so a `u` here would
// restore the cards' old stage names *under the new column list* and strand
// every one of them in the first column. The edit is its own inverse instead —
// renaming QA back to Review carries the same cards back, because stageRemap
// is positional.
func (m *model) applyStageEdit(next []string, icons map[string]string) {
	prev := m.boardCfg.stages
	var probe boardConfig
	probe.setColumns(next, icons)
	if len(prev) == len(next) && sameIcons(m.boardCfg.icons, probe.icons) {
		same := true
		for i := range prev {
			if prev[i] != next[i] {
				same = false
				break
			}
		}
		if same {
			return
		}
	}
	// Collect the affected cards before the list is swapped, while their
	// stored stage names still key into the remap.
	remap := stageRemap(prev, next)
	var touched []*todo.Todo
	if len(remap) > 0 {
		for _, t := range m.tasks {
			if t.Stage == "" {
				continue // already the first column; nothing to carry over
			}
			if _, ok := remap[strings.ToLower(t.Stage)]; ok {
				touched = append(touched, t)
			}
		}
	}
	m.boardCfg.setColumns(next, icons)
	// Stamped here and nowhere else: the timestamp is what wins the list a
	// merge (boardsync.go), so it marks a deliberate edit, never a list that
	// merely arrived from the fleet or was read back off disk.
	m.boardCfg.modifiedAt = time.Now().UTC()
	ids := make([]string, 0, len(touched))
	for _, t := range touched {
		t.SetStage(remap[strings.ToLower(t.Stage)])
		ids = append(ids, t.ID)
	}
	if len(ids) > 0 {
		m.markModified(ids...)
	} else {
		m.markCacheDirty() // the board columns themselves changed
	}
	m.persistSettings()
}

func (m model) updateIdlePrompt(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "k", "esc", "enter":
			m.mode = modeNormal
		case "s":
			if t := m.findTodoByID(m.pendingEntryTaskID); t != nil && t.IsTimerRunning() {
				m.pushUndo("stop timer", t.ID)
				t.StopTimer()
				m.markModified(t.ID)
			}
			m.mode = modeNormal
		case "e":
			return m, m.startEditTimeEntry(m.pendingEntryTaskID, m.pendingEntryID)
		case "d":
			if t := m.findTodoByID(m.pendingEntryTaskID); t != nil {
				for i := range t.TimeEntries {
					if t.TimeEntries[i].ID == m.pendingEntryID {
						m.pushUndo("discard time entry", t.ID)
						t.DeleteTimeEntry(i)
						m.markModified(t.ID)
						break
					}
				}
			}
			m.mode = modeNormal
		}
	}
	return m, nil
}

func (m *model) confirmDeleteTimeEntry() tea.Cmd {
	if t := m.findTodoByID(m.pendingEntryTaskID); t != nil {
		for i := range t.TimeEntries {
			if t.TimeEntries[i].ID == m.pendingEntryID {
				m.pushUndo("delete time entry", t.ID)
				t.DeleteTimeEntry(i)
				m.markModified(t.ID)
				break
			}
		}
	}
	acts := m.activitiesForDay(m.calendar.selected)
	if len(acts) == 0 {
		m.calendar.focusTimeline = false
		m.calendar.entryCursor = 0
	} else if m.calendar.entryCursor >= len(acts) {
		m.calendar.entryCursor = len(acts) - 1
	}
	return nil
}

// confirmDeleteTimeEntryFromDetail is the detail-pane variant of
// confirmDeleteTimeEntry. It deletes the entry and clamps detail.timeEntryCursor
// instead of the calendar's entryCursor, so the two surfaces stay independent.
func (m *model) confirmDeleteTimeEntryFromDetail() tea.Cmd {
	if t := m.findTodoByID(m.pendingEntryTaskID); t != nil {
		for i := range t.TimeEntries {
			if t.TimeEntries[i].ID == m.pendingEntryID {
				m.pushUndo("delete time entry", t.ID)
				t.DeleteTimeEntry(i)
				m.markModified(t.ID)
				break
			}
		}
		// Re-fetch after deletion to get the updated length.
		if t2 := m.findTodoByID(m.pendingEntryTaskID); t2 != nil {
			if m.detail.timeEntryCursor >= len(t2.TimeEntries) && m.detail.timeEntryCursor > 0 {
				m.detail.timeEntryCursor--
			}
		}
	}
	return nil
}

func (m *model) confirmDeleteTag() tea.Cmd {
	if t := m.currentTodo(); t != nil && m.pendingTag < len(t.Tags) {
		m.pushUndo("remove tag", t.ID)
		t.RemoveTag(t.Tags[m.pendingTag])
		if m.detail.tagCursor >= len(t.Tags) && m.detail.tagCursor > 0 {
			m.detail.tagCursor--
		}
		m.markModified(t.ID)
	}
	return nil
}

func (m *model) confirmDeleteTagGlobal() tea.Cmd {
	if tags := m.getFilteredTagsForTab(); m.tagTabCursor < len(tags) {
		m.pushUndo("delete tag globally")
		touched := m.deleteTagGlobally(tags[m.tagTabCursor])
		m.markModified(touched...)
		if remaining := m.getFilteredTagsForTab(); m.tagTabCursor >= len(remaining) && m.tagTabCursor > 0 {
			m.tagTabCursor--
		}
	}
	return nil
}

// confirmDeleteProjectGlobal clears the pending project off every task
// carrying it — the Projects-tab mirror of confirmDeleteTagGlobal. The tasks
// survive; only the grouping goes, which is why it is not a task delete.
func (m *model) confirmDeleteProjectGlobal() tea.Cmd {
	name := m.pendingProjectName
	m.pendingProjectName = ""
	if name == "" {
		return nil
	}
	// A stale row (nothing carries the project any more) must not leave an
	// undo entry that restores nothing.
	carried := false
	for _, t := range m.tasks {
		if t.Project == name {
			carried = true
			break
		}
	}
	if !carried {
		return nil
	}
	m.pushUndo("delete project")
	m.markModified(m.renameProjectGlobally(name, "")...)
	if projects := m.allProjectsForList(); m.projectCursor >= len(projects) && m.projectCursor > 0 {
		m.projectCursor = len(projects) - 1
	}
	return nil
}

func (m *model) confirmDeleteProject() tea.Cmd {
	if t := m.currentTodo(); t != nil {
		m.setProject(t, "", "remove project")
	}
	return nil
}

func (m *model) confirmDeleteSubtask() tea.Cmd {
	if t := m.currentTodo(); t != nil && m.pendingSubtask < m.subtaskCount(t.ID) {
		// Capture the parent (its subtask list will change) plus the subtask +
		// every transitive descendant (so undo can restore the whole tree).
		subID := ""
		if ids := m.subtaskIDs(t.ID); m.pendingSubtask < len(ids) {
			subID = ids[m.pendingSubtask]
		}
		toDelete := m.descendantIDs(subID)
		undoIDs := append([]string{t.ID}, toDelete...)
		m.pushUndo("delete subtask", undoIDs...)
		for _, id := range toDelete {
			m.markTombstone(id)
			m.remove(id)
		}
		if m.detail.subtaskCursor >= m.subtaskCount(t.ID) && m.detail.subtaskCursor > 0 {
			m.detail.subtaskCursor--
		}
		// Tombstone already records what to persist; nothing else changed.
		m.dirty = true
		m.cache.dirty = true
		m.invalidateDetailCache()
		m.refreshCaches()
	}
	return nil
}
