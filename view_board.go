package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// view_board.go renders the Board tab: one kanban column per configured stage
// (settings.json "stages"), the last of which is the Done column. The board is
// a different projection of the same filtered lists the Tasks tab shows —
// cards inherit the active list's sequence order and the done list's recency
// order, and the active search filter applies unchanged.

const (
	// boardColGap is the space between two columns. The card boxes mark each
	// column's edges, so the gap is empty; three cells keep two neighbouring
	// boxes from reading as one.
	boardColGap  = 3
	boardMinColW = 16 // below this per-column width the board degrades to a stacked list
	// boardMinWindowCols is the fewest columns worth scrolling between. A board
	// showing one or two columns of eleven has stopped being a board — the
	// stacked list shows every stage at once and is the better answer on a
	// genuinely narrow window. Scrolling is for the case the stacked list
	// cannot help with: a wide enough terminal, too many stages.
	boardMinWindowCols = 3
	// The Done column reads the (potentially long) history list; anything past
	// this many cards can never be visible, so don't build rows for it.
	boardDoneCards = 50
)

// buildBoardColumns splits the filtered active/done lists into per-column
// card lists: one column per configured stage, with the last one holding
// the done tasks (capped at boardDoneCards) rather than a stage. Pure so
// refreshCaches can derive it and tests can drive it directly.
func buildBoardColumns(c boardConfig, active, done []todo.Todo) [][]todo.Todo {
	cols := make([][]todo.Todo, len(c.stages))
	for i := range active {
		col := c.stageIndex(active[i].Stage)
		cols[col] = append(cols[col], active[i])
	}
	if len(done) > boardDoneCards {
		done = done[:boardDoneCards]
	}
	cols[c.doneColumn()] = append([]todo.Todo(nil), done...)
	return cols
}

// boardColumns returns the cached per-column card lists (rebuilt by
// refreshCaches/refreshFilteredCaches alongside the active/done split — the
// column split copies task values, and doing that per frame is exactly the
// per-frame O(active) work cacheState exists to prevent). The nil-cache
// fallback builds directly so a bare model (tests) stays correct.
func (m model) boardColumns() [][]todo.Todo {
	if m.cache.boardCols != nil {
		return m.cache.boardCols
	}
	return buildBoardColumns(m.boardCfg, m.cache.active, m.cache.done)
}

// boardColTitles returns the column headers — the configured names verbatim,
// the last of which heads the Done column. They are user text, so they are not
// translated: a board whose columns you named is shown the way you named them.
// A column's icon leads its name, the mark its cards carry in the task list.
func (m model) boardColTitles() []string {
	titles := make([]string, len(m.boardCfg.stages))
	for i, name := range m.boardCfg.stages {
		titles[i] = name
		if icon := m.boardCfg.columnIcon(i); icon != "" {
			titles[i] = icon + " " + name
		}
	}
	return titles
}

// boardSelection clamps the stored board cursor against the current columns,
// so a stale position (task completed elsewhere, stage list edited) degrades
// to the nearest valid card instead of pointing past the end.
func (m model) boardSelection(cols [][]todo.Todo) (col, cursor int) {
	col, cursor = m.board.col, m.board.cursor
	if col < 0 {
		col = 0
	}
	if col >= len(cols) {
		col = len(cols) - 1
	}
	if n := len(cols[col]); cursor >= n {
		cursor = n - 1 // -1 on an empty column = no selected card
	}
	if cursor < 0 {
		cursor = 0
	}
	return col, cursor
}

// boardColumnsForView is boardColumns with a held card drawn at the top of the
// column it is over, its own included — a preview only; the store changes when
// the card is put down. The carry cursor sits on row 0, so the held card must
// be the one there.
func (m model) boardColumnsForView() [][]todo.Todo {
	cols := m.boardColumns()
	if m.mode != modeBoardCarry {
		return cols
	}
	from := boardCardColumn(cols, m.board.carryID)
	to := m.board.carryCol
	if from < 0 || to < 0 || to >= len(cols) {
		return cols
	}
	out := make([][]todo.Todo, len(cols))
	copy(out, cols)
	var held todo.Todo
	rest := make([]todo.Todo, 0, len(cols[from]))
	for _, c := range cols[from] {
		if c.ID == m.board.carryID {
			held = c
			continue
		}
		rest = append(rest, c)
	}
	out[from] = rest
	out[to] = append([]todo.Todo{held}, out[to]...)
	return out
}

// boardSelectedTask returns the task under the board cursor, or nil on an
// empty column.
func (m model) boardSelectedTask() *todo.Todo {
	cols := m.boardColumns()
	col, cursor := m.boardSelection(cols)
	if len(cols[col]) == 0 {
		return nil
	}
	return m.get(cols[col][cursor].ID)
}

// boardWindow decides which columns are on screen: the first visible index,
// how many are visible, and their width. Splitting the width across every
// stage is what a three-stage board wants and what a ten-stage one cannot
// survive — eleven columns need a 204-column terminal before each is even
// boardMinColW wide. So the columns that fit are shown at a readable width and
// the rest are scrolled to, one column at a time.
//
// count == 0 means not even one column fits; the caller falls back to the
// stacked layout, which is still the right answer on a genuinely narrow window.
func boardWindow(n, offset, availW int) (start, count, colW int) {
	if n <= 0 {
		return 0, 0, 0
	}
	fit := 0
	for k := 1; k <= n; k++ {
		if (availW-(k-1)*boardColGap)/k < boardMinColW {
			break
		}
		fit = k
	}
	// Too few columns to still read as a board: let the caller stack instead.
	// Capped at n, so a two-stage board is never told it needs three.
	need := boardMinWindowCols
	if need > n {
		need = n
	}
	if fit < need {
		return 0, 0, 0
	}
	count = fit
	start = offset
	if max := n - count; start > max {
		start = max
	}
	if start < 0 {
		start = 0
	}
	return start, count, (availW - (count-1)*boardColGap) / count
}

func (m model) renderBoardList() string {
	if m.mode == modeBoardCard {
		return m.renderBoardCardView()
	}
	cols := m.boardColumnsForView()
	titles := m.boardColTitles()
	n := len(cols)
	g := m.boardGeometry(cols)
	if g.count == 0 {
		return m.renderBoardStacked(cols, titles)
	}
	selCol, selCursor := m.boardSelection(cols)
	rendered := make([][]string, 0, g.count)
	for c := g.start; c < g.start+g.count; c++ {
		cursor, offset := -1, 0
		if c == selCol {
			cursor = selCursor
			if m.board.scrollCol == c {
				offset = m.board.cardScroll
			}
		}
		rendered = append(rendered,
			m.renderBoardColumn(cols[c], titles[c], c == n-1, cursor, g.widths[c-g.start], g.budget, g.layouts[c-g.start], offset))
	}
	return joinBoardColumns(g.widths, g.budget, rendered...)
}

// boardGeometry is the board's layout for the current window: which columns
// are on screen, their widths, the rows each has, and the card layout. The
// render and the scroll clamp both read it, so the rows the clamp keeps the
// cursor inside are the rows that are drawn.
type boardGeom struct {
	start, count int
	widths       []int
	budget       int               // rows per column, heading and rule included
	layouts      []boardCardLayout // per visible column
}

func (m model) boardGeometry(cols [][]todo.Todo) boardGeom {
	availW := m.termWidth - 8
	var g boardGeom
	g.start, g.count, _ = boardWindow(len(cols), m.board.colOffset, availW)
	// Rows available inside the list panel: buildListContent subtracts the two
	// border lines from the outer height, mirrored here so per-column clipping
	// and the scroll markers line up with what actually fits.
	g.budget = m.listVisible() - 2
	if g.budget < 4 {
		g.budget = 4
	}
	if g.count == 0 {
		return g
	}
	// boardColWidths gets the full pane width, not boardWindow's per-column
	// share: that share is an integer division and drops the remainder, which
	// would stop the grid a few columns short of its own border.
	g.widths = boardColWidths(g.count, availW)
	// Each column wraps its titles if it has the room, but boxes are all or
	// nothing across the board, so a short window never draws boxes beside
	// plain rows.
	g.layouts = make([]boardCardLayout, g.count)
	boxed := true
	for c := g.start; c < g.start+g.count; c++ {
		g.layouts[c-g.start] = chooseBoardCardLayout(cols[c], g.widths[c-g.start], g.budget-2)
		boxed = boxed && g.layouts[c-g.start].boxed
	}
	if !boxed {
		for i := range g.layouts {
			g.layouts[i] = boardCardLayout{lines: 1}
		}
	}
	return g
}

// boardCardHeights is the rows each card takes in the given layout.
func boardCardHeights(cards []todo.Todo, colW int, layout boardCardLayout) []int {
	h := make([]int, len(cards))
	for i := range cards {
		h[i] = 1
		switch {
		case layout.boxed && layout.lines == 0:
			h[i] = 2
		case layout.boxed:
			lines, _ := boardCardText(&cards[i], false, colW-boardBoxChrome, layout.lines)
			h[i] = len(lines) + 2
		}
	}
	return h
}

// boardCardWindow decides which cards of a column are drawn: [first, end),
// scrolled from offset only as far as it takes to keep cursor on screen, with
// a row given to an "↑ N more" / "↓ N more" marker on each side that has cards
// past it. Pure, so the clamp in Update and the
// render run the same arithmetic.
func boardCardWindow(heights []int, cursor, offset, room int) (first, end int) {
	n := len(heights)
	if n == 0 {
		return 0, 0
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= n {
		cursor = n - 1
	}
	visibleEnd := func(off int) int {
		rows := room
		if off > 0 {
			rows-- // ↑ marker
		}
		e, used := off, 0
		for e < n && used+heights[e] <= rows {
			used += heights[e]
			e++
		}
		// Cards remain below: the ↓ marker needs a row of its own.
		for e < n && e > off && used+1 > rows {
			e--
			used -= heights[e]
		}
		if e == off {
			e = off + 1 // a card taller than the column still shows, clipped
		}
		return e
	}
	if offset > cursor {
		offset = cursor
	}
	if offset < 0 {
		offset = 0
	}
	for offset < cursor && visibleEnd(offset) <= cursor {
		offset++
	}
	// Scrolled further than the cards need (the column shrank): pull back while
	// everything from there to the last card still fits.
	for offset > 0 && visibleEnd(offset-1) >= n && offset-1 <= cursor {
		offset--
	}
	return offset, visibleEnd(offset)
}

// boardColWidths splits the board's width into an even grid across the
// visible columns, the rounding remainder spread one column at a time so no
// column sits more than a character off its neighbours.
// Even whatever the columns hold, so the board does not shift under a card
// being carried across it.
func boardColWidths(n, availW int) []int {
	widths := make([]int, n)
	if n == 0 {
		return widths
	}
	budget := availW - (n-1)*boardColGap
	even, extra := budget/n, budget%n
	for i := range widths {
		widths[i] = even
		if i < extra {
			widths[i]++
		}
	}
	return widths
}

// joinBoardColumns lays the rendered columns side by side, each padded to its
// width, over height rows. The gaps are plain space: the cards are boxes now,
// and boxes already say where a column is — dividers down every gap on top of
// them made the board a spreadsheet, fencing in rows that were mostly empty.
func joinBoardColumns(widths []int, height int, columns ...[]string) string {
	var b strings.Builder
	gap := strings.Repeat(" ", boardColGap)
	for row := 0; row < height; row++ {
		var line strings.Builder
		for c, col := range columns {
			cell := ""
			if row < len(col) {
				cell = col[row]
			}
			if c == len(columns)-1 {
				line.WriteString(cell)
				continue
			}
			cell = ansi.Truncate(cell, widths[c], "")
			if lw := ansi.StringWidth(cell); lw < widths[c] {
				cell += strings.Repeat(" ", widths[c]-lw)
			}
			line.WriteString(cell + gap)
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// boardCardLayout is how a column draws its cards, chosen per column by how
// much room it has: boxes with the title wrapped onto two lines while the whole
// column fits that way, then one-line boxes while those fit, then compact
// boxes — the title set into the top edge, two rows a card — scrolling when
// even those run past the bottom; plain rows only on a window too short to
// hold a box and its scroll markers. A compact box keeps what a box says (the
// state in its border, project and due in its bottom edge) at two thirds of
// the height, so a long column shows half as many cards again before it
// scrolls.
type boardCardLayout struct {
	boxed bool
	lines int // title lines per card; 0 on a boxed layout is the compact box
}

func chooseBoardCardLayout(cards []todo.Todo, colW, room int) boardCardLayout {
	if colW < boardBoxMinW || room < boardBoxMinRoom {
		return boardCardLayout{lines: 1}
	}
	inner := colW - boardBoxChrome
	if boardCardsFit(cards, inner, 2, room) {
		return boardCardLayout{boxed: true, lines: 2}
	}
	if boardCardsFit(cards, inner, 1, room) {
		return boardCardLayout{boxed: true, lines: 1}
	}
	return boardCardLayout{boxed: true, lines: 0}
}

// boardHeading is a column's heading: its name, and the card count dimmed
// beside it rather than bracketed.
func boardHeading(title string, n, w int) string {
	count := fmt.Sprintf("  %d", n)
	name := truncate(title, w-len([]rune(count)))
	return statsHeaderStyle.Render(name) + dimStyle.Render(count)
}

// renderBoardColumn builds one column's lines: heading with count, a rule, then
// the cards in the board's layout, clipped to the row budget. cursor is the selected card index, or
// -1 when the column isn't focused. doneCol renders its cards dim — they're
// history, not work.
func (m model) renderBoardColumn(cards []todo.Todo, title string, doneCol bool, cursor, colW, budget int, layout boardCardLayout, offset int) []string {
	lines := make([]string, 0, budget)
	// The heading starts where its cards do — after the marker column, which is
	// blank on every row but the selected one — or it hangs out to the left of
	// its own cards.
	indent := cursorGap
	lines = append(lines, indent+boardHeading(title, len(cards), colW-len(indent)))
	// The focused column is marked by an accented rule under its heading — the
	// heading text itself keeps the standard style so it stays legible.
	rule := strings.Repeat("─", colW)
	if m.boardColumnHoldsCarry(cards) {
		lines = append(lines, lipgloss.NewStyle().Foreground(m.carryColor()).Render(rule))
	} else if cursor != -1 {
		lines = append(lines, selectedStyle.Render(rule))
	} else {
		lines = append(lines, dimStyle.Render(rule))
	}
	if len(cards) == 0 {
		lines = append(lines, dimStyle.Render(indent+tr("empty")))
		return lines
	}
	first, end := boardCardWindow(boardCardHeights(cards, colW, layout), cursor, offset, budget-len(lines))
	if first > 0 {
		lines = append(lines, dimStyle.Render(fmt.Sprintf("%s↑ %d %s", indent, first, tr("more"))))
	}
	for i := first; i < end; i++ {
		if layout.boxed {
			lines = append(lines, m.renderBoardBox(&cards[i], doneCol, i == cursor, colW, layout.lines)...)
		} else {
			lines = append(lines, m.renderBoardCard(&cards[i], doneCol, i == cursor, colW, 1)...)
		}
	}
	if end < len(cards) {
		lines = append(lines, dimStyle.Render(fmt.Sprintf("%s↓ %d %s", indent, len(cards)-end, tr("more"))))
	}
	return lines
}

// boardColumnHoldsCarry reports whether the held card is over this column, so
// the column's rule lights with it.
func (m model) boardColumnHoldsCarry(cards []todo.Todo) bool {
	if m.mode != modeBoardCarry || m.board.carryID == "" {
		return false
	}
	for i := range cards {
		if cards[i].ID == m.board.carryID {
			return true
		}
	}
	return false
}

// boardCardBadge is the high-priority "!" the task list uses, on pending cards.
func boardCardBadge(t *todo.Todo, doneCol bool) string {
	if !doneCol && t.Priority == todo.PriorityHigh {
		return " !"
	}
	return ""
}

// boardCardText wraps a card's title into at most maxLines lines of textW,
// leaving room on the last for the badge, so the "!" sits at the end of the
// title however it wrapped rather than being the first thing a narrow column
// clips.
func boardCardText(t *todo.Todo, doneCol bool, textW, maxLines int) (lines []string, badge string) {
	badge = boardCardBadge(t, doneCol)
	textW -= len([]rune(badge))
	if maxLines <= 1 || textW < 1 {
		return []string{truncate(t.Title, textW)}, badge
	}
	return clampLines(wrapText(t.Title, textW), maxLines), badge
}

// boardCardsFit reports whether a column's cards fit room rows as boxes of up
// to maxLines title lines: each box is its title lines plus two borders. It
// stops wrapping titles once the count passes room, so a long column costs a
// few cards' worth of wrapping rather than all of them on every key.
func boardCardsFit(cards []todo.Todo, textW, maxLines, room int) bool {
	need := 2 * len(cards)
	for i := 0; i < len(cards) && need <= room; i++ {
		lines, _ := boardCardText(&cards[i], false, textW, maxLines)
		need += len(lines)
	}
	return need <= room
}

// Box geometry: the cursor marker column the plain rows have too, then a
// border and a space of padding on each side of the title.
const (
	boardBoxChrome = 6
	boardBoxMinW   = boardBoxChrome + 8
	// boardBoxMinRoom is the fewest card rows that hold a one-line box with a
	// scroll marker either side; a shorter column draws plain rows.
	boardBoxMinRoom = 5
)

// Every card keeps its rounded corners whatever state it is in. Unicode has no
// heavy rounded corner, and a square selected card reads as a different kind
// of thing rather than the same card highlighted.
var boardBoxRounded = [6]string{"╭", "╮", "╰", "╯", "─", "│"}

// renderBoardBox draws one card as a box. The border carries the card's state
// the way the row tone does on the Tasks tab — red overdue, the timer colour
// while tracked, dim in Done — and the selected card is drawn bold in the
// selection colour. A card that is held is bold in the carry colour.
func (m model) renderBoardBox(t *todo.Todo, doneCol, selected bool, colW, maxLines int) []string {
	inner := colW - boardBoxChrome
	compact := maxLines == 0
	if compact {
		// The title sits in the top edge between "╭─ " and " ─╮", which
		// costs two cells more than the side borders and padding.
		inner -= 2
		maxLines = 1
	}
	text, badge := boardCardText(t, doneCol, inner, maxLines)
	held := m.carrying(t.ID)

	edge, glyphs := boardBoxRestingColor(t, doneCol), boardBoxRounded
	textStyle := normalStyle
	switch {
	case doneCol:
		textStyle = dimStyle
	case t.IsOverdue():
		textStyle = overdueStyle
	}
	if selected {
		edge = currentTheme.green
	}
	if held {
		edge = m.carryColor()
	}
	border := lipgloss.NewStyle().Foreground(edge)
	if selected || held {
		// The title lights up with its edge, so the card under the cursor
		// reads as one lit object rather than a coloured frame round the
		// same text as every other card. The overdue badge keeps its red.
		border = border.Bold(true)
		textStyle = border
	}
	if held && m.carryGlowDone() {
		text[0] = truncate("✓ "+text[0], inner-len([]rune(badge)))
	}

	// The ▶ sits in the margin beside the selected box's first line, where it
	// sits on a plain row, so every list marks its cursor the same way. A held
	// card has none: its lit frame is the mark, and an arrow as well read as a
	// second thing selected.
	boxW := colW - len([]rune(cursorGap))
	lead := func(i int) string {
		if selected && !held && i == 0 {
			return selectedStyle.Render(cursorMark)
		}
		return cursorGap
	}
	if compact {
		body := textStyle.Render(text[0])
		w := len([]rune(text[0]))
		if badge != "" {
			body += overdueStyle.Render(badge)
			w += len([]rune(badge))
		}
		top := lead(0) + border.Render(glyphs[0]+glyphs[4]+" ") + body +
			border.Render(" "+strings.Repeat(glyphs[4], max(boxW-5-w, 1))+glyphs[1])
		return []string{top, cursorGap + m.boardBoxBottom(t, doneCol, border, glyphs, boxW)}
	}
	out := make([]string, 0, len(text)+2)
	out = append(out, cursorGap+border.Render(glyphs[0]+strings.Repeat(glyphs[4], boxW-2)+glyphs[1]))
	for i, line := range text {
		body := textStyle.Render(line)
		w := len([]rune(line))
		if i == len(text)-1 && badge != "" {
			body += overdueStyle.Render(badge)
			w += len([]rune(badge))
		}
		if w < inner {
			body += strings.Repeat(" ", inner-w)
		}
		out = append(out, lead(i)+border.Render(glyphs[5])+" "+body+" "+border.Render(glyphs[5]))
	}
	out = append(out, cursorGap+m.boardBoxBottom(t, doneCol, border, glyphs, boxW))
	return out
}

// boardBoxBottom is a box's bottom edge, carrying the card's project and how
// far off its due date is — the two facts that decide what to pick up next —
// set into the border, so a card knows them without costing a row: ╰─ House · 2d ─╯.
// The due part is red when overdue; the project is what gives way when the
// box is too narrow for both.
func (m model) boardBoxBottom(t *todo.Todo, doneCol bool, border lipgloss.Style, glyphs [6]string, boxW int) string {
	room := boxW - 6 // corners, one rule cell each side, a space each side
	due := ""
	if !doneCol && !t.DueDate.IsZero() {
		due = formatDueShort(t.DueDate, time.Now())
	}
	proj := t.Project
	sep := " · "
	if proj == "" || due == "" {
		sep = ""
	}
	if w := len([]rune(proj + sep + due)); w > room {
		if keep := room - len([]rune(sep+due)); keep >= 3 {
			proj = truncate(proj, keep)
		} else {
			proj, sep = "", ""
		}
	}
	plain := proj + sep + due
	if plain == "" || len([]rune(plain)) > room {
		return border.Render(glyphs[2] + strings.Repeat(glyphs[4], boxW-2) + glyphs[3])
	}
	dueStyle := dimStyle
	if t.IsOverdue() {
		dueStyle = overdueStyle
	}
	meta := dimStyle.Render(proj+sep) + dueStyle.Render(due)
	if due == "" {
		meta = dimStyle.Render(proj)
	}
	rest := boxW - 4 - 1 - len([]rune(plain)) // corners, lead rule, spaces
	return border.Render(glyphs[2]+glyphs[4]) + " " + meta + " " +
		border.Render(strings.Repeat(glyphs[4], rest)+glyphs[3])
}

// boardBoxRestingColor is a card's border colour when nothing is happening to
// it: the one fact the Tasks tab would colour its row for, or dim.
func boardBoxRestingColor(t *todo.Todo, doneCol bool) lipgloss.Color {
	switch {
	case doneCol:
		return currentTheme.dim
	case t.IsTimerRunning():
		return currentTheme.teal
	case t.IsOverdue():
		return currentTheme.red
	}
	return currentTheme.dim
}

// renderBoardCard renders one card as plain rows — the layout of a column too
// long for boxes, and of the stacked narrow-window board: cursor marker, title
// and the "!" badge. Pending cards take the task list's status tone (overdue,
// timer running, selected); the selected card is padded to the column so its
// highlight is one block. Done-column cards are dim — history, not work.
func (m model) renderBoardCard(t *todo.Todo, doneCol, selected bool, colW, maxLines int) []string {
	text, badge := boardCardText(t, doneCol, colW-len([]rune(cursorGap)), maxLines)
	style := taskRowPalette(t, false, selected).status
	if doneCol {
		style = fastDim
		if selected {
			style = fastSelectedDim
		}
	}
	held := m.carrying(t.ID)
	out := make([]string, len(text))
	for i, line := range text {
		lead := cursorGap
		if i == 0 && selected && !held {
			lead = cursorMark
		}
		if i == 0 && held && m.carryGlowDone() {
			lead = "✓ "
		}
		line = lead + line
		if i == len(text)-1 {
			line += badge
		}
		if selected || held {
			line = padRight(line, colW)
		}
		if held {
			out[i] = m.carryRowStyle().Render(line)
			continue
		}
		out[i] = style.render(line)
	}
	return out
}

// renderBoardStacked is the narrow-terminal fallback: stages as full-width
// sections instead of side-by-side columns. Height clipping is left to
// buildListContent, matching the other full-width tabs.
func (m model) renderBoardStacked(cols [][]todo.Todo, titles []string) string {
	var sb strings.Builder
	availW := m.termWidth - 8
	selCol, selCursor := m.boardSelection(cols)
	for c := range cols {
		if c > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(boardHeading(titles[c], len(cols[c]), availW) + "\n")
		if len(cols[c]) == 0 {
			sb.WriteString(dimStyle.Render("  "+tr("empty")) + "\n")
			continue
		}
		for i := range cols[c] {
			cursor := -1
			if c == selCol {
				cursor = selCursor
			}
			sb.WriteString(strings.Join(m.renderBoardCard(&cols[c][i], c == len(cols)-1, i == cursor, availW, 1), "\n") + "\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// renderBoardCardView is the selected card's fields drawn in place of the
// columns: the whole title, then only the fields that are set — a read view
// has no cursor to walk to an empty one, so an empty row would only be noise.
// Editing stays in one place, the Tasks tab's detail pane, which enter opens.
func (m model) renderBoardCardView() string {
	t := m.boardSelectedTask()
	if t == nil {
		return ""
	}
	w := m.termWidth - 8
	const labelW = 14
	valW := w - labelW
	var lines []string
	for _, l := range wrapText(t.Title, w) {
		lines = append(lines, headerStyle.Render(l))
	}
	lines = append(lines, "")
	// The labels are the detail pane's, translations and all; a colon some of
	// them carry there would be the only punctuation in this column.
	label := func(s string) string {
		return detailLabelStyle.Render(padRight(strings.TrimSuffix(tr(s), ":"), labelW))
	}
	field := func(name, value string, style lipgloss.Style) {
		if value == "" {
			return
		}
		lines = append(lines, label(name)+style.Render(truncate(value, valW)))
	}
	col, _ := m.boardSelection(m.boardColumns())
	field("Stage", m.boardColTitles()[col], normalStyle)
	if !t.DueDate.IsZero() {
		due := t.DueDate.Format("02-01-06") + "  (" + formatDueShort(t.DueDate, time.Now()) + ")"
		style := normalStyle
		if t.IsOverdue() {
			style = overdueStyle
		}
		field("Due date", due, style)
	}
	if !t.StartDate.IsZero() {
		field("Start date", formatStartDate(t.StartDate), normalStyle)
	}
	field("Priority", t.Priority.Icon()+" "+trPriority(t.Priority), normalStyle)
	field("Size", trSize(t.Size), normalStyle)
	field("Project", t.Project, projLabelStyle)
	if len(t.Tags) > 0 {
		tags, _ := renderTaskTagsClipped(t.Tags, valW+1, false)
		lines = append(lines, label("Tags:")+strings.TrimPrefix(tags, " "))
	}
	if t.Recurrence != "" {
		field("Recurrence", "↻ "+trRecurrence(t.Recurrence), normalStyle)
	}
	if spent := t.TotalTimeSpent(); spent > 0 {
		field("Time spent:", formatDuration(spent), timerStyle)
	}

	section := func(title string) {
		lines = append(lines, "", detailLabelStyle.Render(tr(title)))
	}
	if subs := m.subtaskIDs(t.ID); len(subs) > 0 {
		section("Subtasks:")
		for _, id := range subs {
			if s := m.get(id); s != nil {
				mark, style := "·", normalStyle
				if s.Status == todo.Done {
					mark, style = "✓", dimStyle
				}
				lines = append(lines, style.Render(truncate("  "+mark+" "+s.Title, w)))
			}
		}
	}
	if len(t.Dependencies) > 0 {
		section("Dependencies:")
		for _, id := range t.Dependencies {
			if d := m.get(id); d != nil {
				lines = append(lines, normalStyle.Render(truncate("  · "+d.Title, w)))
			}
		}
	}
	if strings.TrimSpace(t.Notes) != "" {
		section("Description")
		for _, para := range strings.Split(strings.TrimRight(t.Notes, "\n"), "\n") {
			for _, l := range wrapText(para, w-2) {
				lines = append(lines, normalStyle.Render("  "+l))
			}
		}
	}
	if n := len(t.Comments); n > 0 {
		section("Comments:")
		for _, c := range t.Comments[max(0, n-3):] {
			lines = append(lines, normalStyle.Render(truncate("  · "+c.Text, w)))
		}
	}

	// The pane is as tall as the board; a card with long notes is cut with the
	// ellipsis rather than scrolled — the whole note is one enter away.
	if budget := m.listVisible() - 2; budget > 0 && len(lines) > budget {
		lines = lines[:budget]
		lines[budget-1] = dimStyle.Render("  " + ellipsis)
	}
	return strings.Join(lines, "\n")
}
