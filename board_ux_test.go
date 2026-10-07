package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
	"github.com/charmbracelet/x/ansi"
)

// board_ux_test.go covers the three things that make a many-stage board
// usable: a column window that scrolls, a Stage field in the detail pane, and
// a switch that removes the whole surface for people who never use it.

// manyStages gives m a board of n columns — n-1 working ones plus the Done
// column that always ends the list.
func manyStages(m *model, n int) {
	stages := make([]string, 0, n)
	for _, name := range []string{"Active", "On hold", "Awaiting", "Review", "Blocked",
		"Cancelled", "Later", "Icebox", "Triage", "Ready"} {
		if len(stages) == n-1 {
			break
		}
		stages = append(stages, name)
	}
	m.boardCfg.setStages(append(stages, "Done"))
}

// ── The column window ─────────────────────────────────────────────────────────

// Eleven columns do not fit on any ordinary terminal, and shrinking them all to
// four characters serves nobody. The window shows what fits and scrolls.
func TestBoardWindowShowsWhatFitsAndScrolls(t *testing.T) {
	// Everything fits: no window, and the offset is pinned at zero.
	if start, count, _ := boardWindow(4, 0, 112); start != 0 || count != 4 {
		t.Errorf("4 columns in 112: start %d count %d, want the whole board", start, count)
	}
	// Too many: a window, and it honours the offset.
	start, count, colW := boardWindow(11, 3, 112)
	if count >= 11 || count < boardMinWindowCols {
		t.Fatalf("11 columns in 112: count %d, want a readable window", count)
	}
	if start != 3 {
		t.Errorf("start = %d, want the requested offset", start)
	}
	if colW < boardMinColW {
		t.Errorf("colW = %d, below the readable floor %d", colW, boardMinColW)
	}
	// The offset cannot scroll past the end.
	if start, _, _ := boardWindow(11, 99, 112); start != 11-count {
		t.Errorf("start = %d for a runaway offset, want the last full window", start)
	}
	// Too narrow for even a few columns: the caller stacks instead.
	if _, count, _ := boardWindow(11, 0, 42); count != 0 {
		t.Errorf("count = %d on a narrow window, want the stacked fallback", count)
	}
}

// The arrows move focus; the window follows only when focus would leave it.
// That is what makes it scroll a column at a time rather than jump.
func TestBoardWindowFollowsTheFocusedColumn(t *testing.T) {
	m := modelWithTasks(t, todo.New("a card"))
	manyStages(&m, 11)
	m.termWidth, m.termHeight = 120, 30
	m.switchTab(tabBoard)
	m.markCacheDirty()
	m.ensureCache()

	cols := m.boardColumns()
	_, count, _ := boardWindow(len(cols), 0, m.termWidth-8)
	if count == 0 || count >= len(cols) {
		t.Fatalf("test needs a scrolling board: %d of %d columns visible", count, len(cols))
	}

	// Walking right stays put until the focus reaches the edge, then scrolls
	// one column per step.
	for i := 0; i < count-1; i++ {
		m = sendKey(t, m, "right")
		if m.board.colOffset != 0 {
			t.Fatalf("offset moved to %d while the focus was still on screen", m.board.colOffset)
		}
	}
	m = sendKey(t, m, "right")
	if m.board.colOffset != 1 {
		t.Errorf("offset = %d after stepping past the edge, want 1", m.board.colOffset)
	}
	// And back again.
	for i := 0; i < count; i++ {
		m = sendKey(t, m, "left")
	}
	if m.board.colOffset != 0 {
		t.Errorf("offset = %d after walking back, want 0", m.board.colOffset)
	}
}

// A scrolled board must say so, or it just looks like a board missing columns.
func TestBoardTitleNamesTheVisibleSlice(t *testing.T) {
	m := modelWithTasks(t, todo.New("a card"))
	manyStages(&m, 11)
	m.termWidth, m.termHeight = 120, 30
	m.switchTab(tabBoard)
	m.markCacheDirty()
	m.ensureCache()
	if title := m.listPanelTitle(); !strings.Contains(title, "/11") {
		t.Errorf("panel title %q does not say which slice of 11 columns is shown", title)
	}
}

// ── The Stage field ───────────────────────────────────────────────────────────

func stageFieldModel(t *testing.T) model {
	t.Helper()
	m := modelWithTasks(t, todo.New("Fix the boiler"))
	m.switchTab(tabTasks)
	m.pane = paneDetail
	if task := m.currentTodo(); task != nil {
		m.detailTaskID = task.ID
	}
	m.detail.field = fieldStage
	return m
}

// ←/→ change the value on the Stage row, the way they do on a Settings row.
func TestStageFieldCyclesWithTheArrows(t *testing.T) {
	m := stageFieldModel(t)
	task := m.currentTodo()
	if !m.boardCfg.stageFieldVisible(task) {
		t.Fatal("a pending top-level task has no Stage field")
	}
	start := m.boardCfg.stageIndex(task.Stage)

	m = sendKey(t, m, "right")
	if got := m.boardCfg.stageIndex(m.get(task.ID).Stage); got != (start+1)%len(m.boardCfg.pending()) {
		t.Errorf("→ moved to stage %d, want %d", got, (start+1)%len(m.boardCfg.pending()))
	}
	m = sendKey(t, m, "left")
	if got := m.boardCfg.stageIndex(m.get(task.ID).Stage); got != start {
		t.Errorf("← did not undo →: stage %d, want %d", got, start)
	}
	// It wraps rather than stopping at the ends, and never reaches the last
	// column — completing a task has one path, and it is not this one.
	for i := 0; i < len(m.boardCfg.stages)+2; i++ {
		m = sendKey(t, m, "right")
		if m.get(task.ID).Status != todo.Pending {
			t.Fatal("cycling the stage completed the task")
		}
	}
}

// Everywhere else in the pane the arrows still jump section.
func TestArrowsStillJumpSectionOffTheStageRow(t *testing.T) {
	m := stageFieldModel(t)
	m.detail.field = fieldPriority
	m = sendKey(t, m, "right")
	if m.detail.field == fieldPriority {
		t.Error("→ on a non-Stage field did not jump section")
	}
}

// A subtask never reaches the board and Done is a status, not a stage.
func TestStageFieldHidesWhereItWouldLie(t *testing.T) {
	parent := todo.New("parent")
	parent.ID = "p"
	sub := todo.New("subtask")
	sub.ID = "s"
	sub.ParentID = "p"
	done := todo.New("finished")
	done.ID = "d"
	done.Status = todo.Done
	m := modelWithTasks(t, parent, sub, done)

	if !m.boardCfg.stageFieldVisible(m.get("p")) {
		t.Error("a pending top-level task should have the field")
	}
	if m.boardCfg.stageFieldVisible(m.get("s")) {
		t.Error("a subtask has no stage — it never reaches the board")
	}
	if m.boardCfg.stageFieldVisible(m.get("d")) {
		t.Error("a done task has no stage — Done is a status")
	}
}

// Up/down must step over the row when it is not there, or the cursor lands on
// a field the pane is not drawing.
func TestDetailNavigationSkipsAHiddenStageRow(t *testing.T) {
	m := stageFieldModel(t)
	m.boardCfg.shown = false

	m.detail.field = fieldSize
	m = sendKey(t, m, "down")
	if m.detail.field == fieldStage {
		t.Error("↓ landed on the Stage row while the board is off")
	}
	m.detail.field = fieldProject
	m = sendKey(t, m, "up")
	if m.detail.field == fieldStage {
		t.Error("↑ landed on the Stage row while the board is off")
	}
}

// ── Turning the board off ─────────────────────────────────────────────────────

// Off means gone: out of the bar, out of tab cycling, off its digit, and not
// somewhere the cursor can be left standing.
func TestTurningTheBoardOffRemovesTheWholeSurface(t *testing.T) {
	// toggleShowBoard persists, so this needs its own home — writing the shared
	// one would leave every later model in the binary loading a settings.json
	// with the board off.
	setTestHome(t, t.TempDir())
	if err := ensureStorageDir(); err != nil {
		t.Fatal(err)
	}
	m := modelWithTasks(t, todo.New("a card"))
	m.termWidth, m.termHeight = 120, 30
	m.switchTab(tabBoard)

	m.toggleShowBoard()
	if m.boardCfg.shown {
		t.Fatal("the toggle did not turn the board off")
	}
	if m.tab == tabBoard {
		t.Error("the cursor was left standing on a tab that is no longer shown")
	}
	if m.boardCfg.tabVisible(tabBoard) {
		t.Error("tabVisible still reports the board")
	}
	if _, ok := m.tabForNumberKey("5"); ok {
		t.Error("5 still lands on the hidden board")
	}
	// Cycling must step over it rather than stopping there.
	seen := map[tab]bool{}
	cur := tabTasks
	for i := 0; i < numTabs*2; i++ {
		cur = m.boardCfg.nextTab(cur, 1)
		seen[cur] = true
	}
	if seen[tabBoard] {
		t.Error("tab cycling still stops on the board")
	}
	if bar := ansi.Strip(m.renderTabs(200)); strings.Contains(bar, "Board") {
		t.Errorf("the tab bar still shows the board: %q", bar)
	}
	// The numbers do not renumber — Stats stays 6 whether or not the board is on.
	if got, ok := m.tabForNumberKey("6"); !ok || got != tabStats {
		t.Error("hiding the board renumbered the other tabs")
	}
}

// The setting has to survive a restart, or the tab comes back on next launch.
func TestBoardVisibilityPersists(t *testing.T) {
	m := settingsModel(t)
	m.settingsCursor = settingShowBoard
	m = sendKey(t, m, "enter")

	if m.boardCfg.shown {
		t.Fatal("enter on the row did not toggle it")
	}
	got, err := loadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !got.BoardDisabled {
		t.Error("settings.json did not record that the board is off")
	}
	// And the flag is negative, so an untouched file leaves the board on.
	if !boardConfigFromSettings(appSettings{}).shown {
		t.Error("a settings file with no board key should leave the board on")
	}
}

// ── Column widths ────────────────────────────────────────────────────────────

// When there is not enough width to give every column its floor, the split
// falls back to even columns rather than handing some column a negative width.
func TestBoardColumnWidthsFallBackWhenCrowded(t *testing.T) {
	availW := boardMinColW*2 + 3*boardColGap
	got := boardColWidths(4, availW)
	sum := 3 * boardColGap
	for _, w := range got {
		if w <= 0 {
			t.Fatalf("crowded board produced a non-positive width: %v", got)
		}
		sum += w
	}
	if sum != availW {
		t.Errorf("columns + gaps = %d, want %d: %v", sum, availW, got)
	}
}

// A board whose columns change width when a card is added or moved reads as
// clutter, and shifts under a card being carried, so the grid is always even.
func TestBoardColumnsAreAnEvenGrid(t *testing.T) {
	cols := make([][]todo.Todo, 4)
	const availW = 119 // not divisible: the remainder is spread, not dumped
	got := boardColWidths(len(cols), availW)
	lo, hi := got[0], got[0]
	for _, w := range got {
		if w < lo {
			lo = w
		}
		if w > hi {
			hi = w
		}
	}
	if hi-lo > 1 {
		t.Errorf("columns %v differ by %d; the grid should be even", got, hi-lo)
	}
	sum := (len(cols) - 1) * boardColGap
	for _, w := range got {
		sum += w
	}
	if sum != availW {
		t.Errorf("columns + gaps = %d, want exactly the available %d: %v", sum, availW, got)
	}
}

// The heading sits over its own cards. The card marker column is blank on every
// row but the selected one, so a flush-left heading hung two characters to the
// left of every title under it.
func TestBoardHeadingsLineUpWithTheirCards(t *testing.T) {
	a := todo.New("alpha")
	b := todo.New("beta")
	b.Stage = "In progress"
	m := newTagModel(a, b)
	m.tab = tabBoard
	m.termWidth, m.termHeight = 120, 30
	m.refreshCaches()

	lines := strings.Split(ansi.Strip(m.renderBoardList()), "\n")
	if len(lines) < 3 {
		t.Fatalf("board rendered %d lines, want a header, a rule and cards", len(lines))
	}
	header, cards := lines[0], lines[2]
	for _, title := range []string{"In progress", "Beta"} {
		if !strings.Contains(strings.Join(lines, "\n"), title) {
			t.Fatalf("board is missing %q:\n%s", title, strings.Join(lines, "\n"))
		}
	}
	// Display columns, not byte offsets: the card row carries the ▶ marker and
	// the box-drawing borders, which are three bytes each.
	// The last box on the card row is In progress's; Backlog's selected
	// card is rounded too, so the first ╭ is not it.
	col := func(line, sub string) int { return ansi.StringWidth(line[:strings.LastIndex(line, sub)]) }
	if h, c := col(header, "In progress"), col(cards, "╭"); h != c {
		t.Errorf("heading starts at column %d but its card's box at %d:\n%s", h, c, strings.Join(lines, "\n"))
	}
}

// A column with room draws each card as a box with its title wrapped onto two
// lines; a column too long for two-line boxes tries one-line boxes, and one too
// long for boxes at all falls back to one clipped row per card.
func TestBoardCardsAreBoxesWhileTheyFit(t *testing.T) {
	long := todo.New("Draft the quarterly budget proposal for the board meeting")
	short := todo.New("Buy filters")
	short.Priority = todo.PriorityLow // ranks below: equal scores would sort by random ID
	m := newTagModel(long, short)
	m.tab = tabBoard
	m.termWidth, m.termHeight = 80, 30
	m.refreshCaches()

	// The first column's rows, trimmed: the board is laid out on a fixed grid,
	// so the first column is the first colW cells of every row.
	firstCol := func(m model) []string {
		widths := boardColWidths(3, m.termWidth-8)
		lines := strings.Split(ansi.Strip(m.renderBoardList()), "\n")
		out := make([]string, 0, len(lines))
		for _, l := range lines[2:] {
			r := []rune(l)
			if len(r) > widths[0] {
				r = r[:widths[0]]
			}
			out = append(out, strings.TrimSpace(string(r)))
		}
		return out
	}
	got := firstCol(m)
	if !strings.HasPrefix(got[0], "╭") || !strings.HasPrefix(got[1], "▶ │ Draft") ||
		!strings.HasPrefix(got[2], "│") || !strings.HasPrefix(got[3], "╰") {
		t.Fatalf("selected long card should be a rounded two-line box:\n%s", strings.Join(got[:4], "\n"))
	}
	if !strings.HasPrefix(got[4], "╭") || !strings.Contains(got[5], "Buy filters") || !strings.HasPrefix(got[6], "╰") {
		t.Fatalf("the next card should be its own rounded box:\n%s", strings.Join(got[:7], "\n"))
	}

	m.termHeight = 15 // room for one-line boxes only
	got = firstCol(m)
	if !strings.HasPrefix(got[1], "▶ │ Draft") || !strings.HasSuffix(got[1], ellipsis+" │") || !strings.HasPrefix(got[2], "╰") {
		t.Fatalf("a shorter column should clip the title inside a one-line box:\n%s", strings.Join(got, "\n"))
	}

	m.termHeight = 8 // too short for boxes
	got = firstCol(m)
	if !strings.HasPrefix(got[0], "▶ Draft") || !strings.Contains(got[1], "Buy filters") {
		t.Fatalf("a crowded column should fall back to plain rows:\n%s", strings.Join(got, "\n"))
	}
}

// ── Scrolling, card edges, adding, the card view ────────────────────────────

func TestBoardCardWindowKeepsTheCursorOnScreen(t *testing.T) {
	h := []int{3, 3, 3, 3, 3, 3, 3, 3} // eight one-line boxes
	const room = 11

	first, end := boardCardWindow(h, 0, 0, room)
	if first != 0 || end != 3 {
		t.Fatalf("top of the column: window [%d,%d), want [0,3) with a row for ↓", first, end)
	}
	first, end = boardCardWindow(h, 5, 0, room)
	if !(first <= 5 && 5 < end) || first == 0 {
		t.Fatalf("cursor 5 should scroll into view: window [%d,%d)", first, end)
	}
	// Moving back up scrolls only once the cursor passes the top.
	if f, _ := boardCardWindow(h, first, first, room); f != first {
		t.Errorf("cursor at the top of the window moved it: %d → %d", first, f)
	}
	if f, _ := boardCardWindow(h, first-1, first, room); f != first-1 {
		t.Errorf("cursor above the window should scroll by one: got first=%d, want %d", f, first-1)
	}
	// The last card: no ↓ marker, so the window reaches the end.
	if _, e := boardCardWindow(h, 7, 0, room); e != len(h) {
		t.Errorf("cursor on the last card: end = %d, want %d", e, len(h))
	}
	// A column that shrank pulls a stale offset back.
	if f, _ := boardCardWindow(h[:3], 0, 2, room); f != 0 {
		t.Errorf("three cards fit, but the window starts at %d", f)
	}
}

func TestBoardLongColumnScrollsToTheSelectedCard(t *testing.T) {
	var cards []todo.Todo
	for i := 0; i < 12; i++ {
		cards = append(cards, todo.New(fmt.Sprintf("card %02d", i)))
	}
	m := newTagModel(cards...)
	m.tab = tabBoard
	m.termWidth, m.termHeight = 100, 24
	m.refreshCaches()
	for i := 0; i < 11; i++ {
		m = sendKey(t, m, "down")
	}
	out := ansi.Strip(m.renderBoardList())
	sel := m.boardSelectedTask()
	var marked string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "▶") {
			marked = l
		}
	}
	if !strings.Contains(marked, sel.Title) {
		t.Fatalf("selected %q is not on screen:\n%s", sel.Title, out)
	}
	if !strings.Contains(out, "↑ ") {
		t.Errorf("a scrolled column should say how many cards are above:\n%s", out)
	}
}

func TestBoardBoxEdgeCarriesProjectAndDue(t *testing.T) {
	late := todo.New("late one")
	late.Project = "House"
	late.DueDate = startOfDay(time.Now()).AddDate(0, 0, -2)
	m := newTagModel(late)
	m.tab = tabBoard
	m.termWidth, m.termHeight = 120, 30
	m.refreshCaches()
	out := ansi.Strip(m.renderBoardList())
	if !strings.Contains(out, "╰─ House · -2d ─") {
		t.Fatalf("the box's bottom edge should carry project and due:\n%s", out)
	}
}

func TestScriptBoardAddFilesIntoTheFocusedColumn(t *testing.T) {
	m := modelWithTasks(t)
	m = script(t, m, "5", "right", "a", "call the plumber", "enter")
	var added *todo.Todo
	for _, x := range m.allTodos() {
		added = m.get(x.ID)
	}
	if added == nil || added.Stage != "In progress" {
		t.Fatalf("a card added with In progress focused should land there, got %+v", added)
	}
	if sel := m.boardSelectedTask(); m.board.col != 1 || sel == nil || sel.ID != added.ID || m.pane != paneList {
		t.Errorf("the board should stay put with the new card selected: col %d selected %v pane %v",
			m.board.col, sel, m.pane)
	}

	// From Done, a new card lands in the first column.
	m = script(t, m, "right", "right", "a", "second", "enter")
	for _, x := range m.allTodos() {
		if x.Title == "Second" && (x.Stage != "" || x.Status != todo.Pending) {
			t.Fatalf("a card added from Done should be a pending first-column card: stage %q status %v", x.Stage, x.Status)
		}
	}
	if m.board.col != 0 {
		t.Errorf("focus should follow the card into the first column, col = %d", m.board.col)
	}
}

func TestScriptBoardCardView(t *testing.T) {
	a := todo.New("first card")
	a.Notes = "the whole note"
	b := todo.New("second card")
	m := modelWithTasks(t, a, b)
	m = script(t, m, "5", " ")
	if m.mode != modeBoardCard {
		t.Fatalf("space should open the card view, mode = %v", m.mode)
	}
	first := m.boardSelectedTask().Title
	if out := ansi.Strip(m.View()); !strings.Contains(out, first) || !strings.Contains(out, "Card") {
		t.Fatalf("card view should show the card:\n%s", out)
	}
	m = script(t, m, "down")
	if m.mode != modeBoardCard || m.boardSelectedTask().Title == first {
		t.Fatalf("↓ should step to the next card without closing the view")
	}
	m = script(t, m, "esc")
	if m.mode != modeNormal || m.tab != tabBoard {
		t.Fatalf("esc should close the card view back onto the board")
	}
	m = script(t, m, " ")
	want := m.boardSelectedTask().ID
	m = script(t, m, "enter")
	if m.tab != tabTasks || m.pane != paneDetail || m.currentTodo() == nil || m.currentTodo().ID != want {
		t.Fatalf("enter should open the card in the Tasks detail pane: tab %v pane %v", m.tab, m.pane)
	}
}

// A column too long for one-line boxes steps down to compact boxes — the
// title set into the top edge, two rows a card — before it scrolls, so a
// short window shows half as many cards again and every one keeps its border
// and its bottom edge.
func TestBoardLongColumnUsesCompactBoxes(t *testing.T) {
	var cards []todo.Todo
	for i := 0; i < 9; i++ {
		cards = append(cards, todo.New(fmt.Sprintf("Card %d", i)))
	}
	if l := chooseBoardCardLayout(cards[:3], 24, 16); !l.boxed || l.lines == 0 {
		t.Errorf("three cards fit full boxes in 16 rows, got %+v", l)
	}
	l := chooseBoardCardLayout(cards, 24, 16)
	if !l.boxed || l.lines != 0 {
		t.Fatalf("nine cards in 16 rows should be compact boxes, got %+v", l)
	}
	if h := boardCardHeights(cards, 24, l); h[0] != 2 {
		t.Errorf("a compact box is %d rows, want 2", h[0])
	}
	m := newTestModel()
	box := m.renderBoardBox(&cards[0], false, true, 24, 0)
	if len(box) != 2 {
		t.Fatalf("compact box drew %d lines, want 2: %q", len(box), box)
	}
	top := ansi.Strip(box[0])
	if !strings.Contains(top, "╭─ Card 0") || !strings.HasSuffix(top, "╮") {
		t.Errorf("the title should sit in the top edge: %q", top)
	}
	for i, line := range box {
		if w := ansi.StringWidth(line); w != 24 {
			t.Errorf("line %d is %d cells, want the column's 24: %q", i, w, ansi.Strip(line))
		}
	}
}
