package main

import (
	"fmt"

	"github.com/Morteningemann86/xtratjek/rank"
	"github.com/Morteningemann86/xtratjek/todo"
	tea "github.com/charmbracelet/bubbletea"
)

// update_board.go — Board tab interactions: column focus, card cursor, and
// moving cards between stages. Completion is shared with the Tasks tab's 'd'
// via closePendingTask, so a card moved into Done follows the exact same
// close path (timer stop, open-subtask confirm, rank capture, recurrence).

// closePendingTask runs the full pending→done mutation for t: stops its
// running timer, handles the open-subtask confirm/cascade, records undo,
// captures the sequence rank, toggles, spawns the next recurrence, and
// cascades done-ness to ancestors. Returns false when a confirm prompt was
// staged instead of mutating (open subtasks without auto-close) — the confirm
// flow completes the close on its own.
func (m *model) closePendingTask(t *todo.Todo) bool {
	// Closing a task while its timer is running would leave a dangling open
	// entry — and the runningTimers index would go stale. Stop first, then
	// toggle. Mirrors the CLI done path.
	if t.IsTimerRunning() {
		m.stopTimer(t.ID)
	}
	isSub := t.ParentID != ""
	// Pending parent with open subtasks: with auto-close-subtasks on, cascade
	// them closed; otherwise stage a confirm rather than silently close (and
	// hide) the open work.
	cascadeSubs := false
	if !isSub {
		if done, total := m.subtaskProgress(t.ID); total > 0 && done < total {
			if m.autoCloseSubtasks {
				cascadeSubs = true
			} else {
				m.pendingCloseParentID = t.ID
				m.mode = modeConfirm
				m.confirmOnYes = (*model).confirmCloseParent
				m.confirmMsg = fmt.Sprintf(tr("Close '%s' with %d open subtask(s)? (y/n)"), truncate(t.Title, 40), total-done)
				return false
			}
		}
	}
	// Full snapshot: ancestor cascade + recurrence spawn can touch arbitrary
	// IDs not knowable until mid-mutation, so capture all state for a clean
	// undo.
	if isSub || cascadeSubs || t.IsRecurring() {
		m.pushUndo("close task")
	} else {
		m.pushUndo("toggle done", t.ID)
	}
	rank.CaptureRankAtDone(m.rank, m.allTodos(), t)
	t.Toggle()
	ids := []string{t.ID}
	if t.IsRecurring() {
		if newID := m.spawnNextRecurrence(t); newID != "" {
			ids = append(ids, newID)
		}
	}
	if isSub {
		ids = append(ids, m.autoCloseAncestorsIfAllDone(t.ID)...)
	}
	if cascadeSubs {
		ids = append(ids, m.closePendingSubtree(t.ID)...)
	}
	m.markModified(ids...)
	return true
}

// stageReopenConfirm stages the "move back to active?" prompt for a done
// task. Confirmed rather than immediate because reopening voids the
// completion-rank reading — same rule for a stray 'd' on a completed row and
// for a board card moved out of the Done column.
func (m *model) stageReopenConfirm(t *todo.Todo) {
	m.pendingReopenID = t.ID
	m.mode = modeConfirm
	m.confirmOnYes = (*model).confirmReopen
	m.confirmMsg = fmt.Sprintf(tr("Move '%s' to active? (y/n)"), truncate(t.Title, 40))
}

// boardMoveCursor moves the card cursor within the focused column, wrapping
// like the other list tabs.
func (m *model) boardMoveCursor(delta int) {
	cols := m.boardColumns()
	col, cursor := m.boardSelection(cols)
	n := len(cols[col])
	if n == 0 {
		return
	}
	m.board.col = col
	m.board.cursor = (cursor + delta + n) % n
}

// boardMoveColumn moves the focus between columns, clamping at the edges. The
// cursor keeps its row where possible (boardSelection clamps it to the new
// column's length at render time).
func (m *model) boardMoveColumn(delta int) {
	cols := m.boardColumns()
	col, cursor := m.boardSelection(cols)
	col += delta
	if col < 0 {
		col = 0
	}
	if col >= len(cols) {
		col = len(cols) - 1
	}
	m.board.col, m.board.cursor = col, cursor
}

// boardFollow points the board cursor at the card with the given ID inside
// column col, falling back to the top when it isn't there (filtered out, or
// hidden past the overflow cap).
func (m *model) boardFollow(col int, id string) {
	m.board.col, m.board.cursor = col, 0
	cols := m.boardColumns()
	if col < 0 || col >= len(cols) {
		return
	}
	for i := range cols[col] {
		if cols[col][i].ID == id {
			m.board.cursor = i
			return
		}
	}
}

// boardMoveCard moves the selected card one column left or right: between
// stages it's a stage edit (undoable), into the last column it completes the
// task via the shared close path, and out of it stages the reopen confirm —
// the card then reappears in its stored stage.
func (m *model) boardMoveCard(dir int) {
	cols := m.boardColumns()
	col, _ := m.boardSelection(cols)
	t := m.boardSelectedTask()
	if t == nil {
		return
	}
	doneCol := m.boardCfg.doneColumn()
	target := col + dir
	if target < 0 || target > doneCol {
		return
	}
	if col == doneCol {
		m.stageReopenConfirm(t)
		return
	}
	m.boardPlaceCard(t, target)
}

// boardPlaceCard puts a pending card into column target: a stage edit
// (undoable) for a working column, the shared close path for Done.
func (m *model) boardPlaceCard(t *todo.Todo, target int) {
	doneCol := m.boardCfg.doneColumn()
	if target == doneCol {
		if m.closePendingTask(t) {
			m.boardFollow(doneCol, t.ID)
		}
		return
	}
	m.pushUndo("move stage", t.ID)
	t.SetStage(m.boardCfg.pending()[target])
	m.markModified(t.ID)
	m.boardFollow(target, t.ID)
}

// boardCardColumn is the column the card with the given ID sits in, or -1.
func boardCardColumn(cols [][]todo.Todo, id string) int {
	for c := range cols {
		for i := range cols[c] {
			if cols[c][i].ID == id {
				return c
			}
		}
	}
	return -1
}

// startBoardCarry picks up the selected card. A done card has nowhere to be
// carried but out of Done, which is the reopen question, so it gets that
// prompt directly — the same answer d gives on it.
func (m *model) startBoardCarry() {
	t := m.boardSelectedTask()
	if t == nil {
		return
	}
	if t.Status != todo.Pending {
		m.stageReopenConfirm(t)
		return
	}
	col, _ := m.boardSelection(m.boardColumns())
	m.mode = modeBoardCarry
	m.board.carryID, m.board.carryCol = t.ID, col
	m.board.col, m.board.cursor = col, 0
}

// updateBoardCarry moves the held card between columns. Nothing is stored
// until it is put down, so carrying it across Done on the way to somewhere
// else completes nothing, and the whole trip is one undo step.
func (m model) updateBoardCarry(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "left", "h", "H", "shift+left":
		if m.board.carryCol > 0 {
			m.board.carryCol--
		}
	case "right", "l", "L", "shift+right":
		if m.board.carryCol < m.boardCfg.doneColumn() {
			m.board.carryCol++
		}
	case "enter", "esc":
		m.dropBoardCarry()
		return m, nil
	}
	m.board.col, m.board.cursor = m.board.carryCol, 0
	return m, nil
}

// dropBoardCarry puts the held card down in the column it is over.
func (m *model) dropBoardCarry() {
	id, target := m.board.carryID, m.board.carryCol
	m.mode = modeNormal
	m.board.carryID = ""
	t := m.get(id)
	if t == nil || t.Status != todo.Pending {
		return // gone while held (a sync or reload removed or closed it)
	}
	if from := boardCardColumn(m.boardColumns(), id); from == target || from < 0 {
		m.boardFollow(from, id)
		return
	}
	m.boardPlaceCard(t, target)
}

// carrying reports whether the card with the given ID is the one held up.
func (m model) carrying(id string) bool {
	return m.mode == modeBoardCarry && id != "" && id == m.board.carryID
}

// updateBoardCard is the read-only card view. ↑/↓ step through the column the
// card is in without closing it, enter opens the card in the Tasks tab's
// detail pane — where every field can be edited — and esc or space close it.
func (m model) updateBoardCard(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "up", "k":
		m.boardMoveCursor(-1)
	case "down", "j":
		m.boardMoveCursor(1)
	case "enter":
		t := m.boardSelectedTask()
		m.mode = modeNormal
		if t == nil {
			return m, nil
		}
		m.switchTab(tabTasks)
		m.followTask(t.ID)
		m.detailTaskID = t.ID
		m.detailStack = nil
		m.pane = paneDetail
		m.detail = detailState{field: fieldStartDate}
		m.invalidateDetailCache()
		m.pushFocus(stateDetailPane)
	case "esc", " ", "q":
		m.mode = modeNormal
	}
	if m.boardSelectedTask() == nil && m.mode == modeBoardCard {
		m.mode = modeNormal // the card went away underneath (a sync, a reload)
	}
	return m, nil
}
