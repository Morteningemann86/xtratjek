package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Morteningemann86/xtratjek/paths"
	"github.com/Morteningemann86/xtratjek/todo"
	"github.com/charmbracelet/x/ansi"
)

// ── Pure utilities ────────────────────────────────────────────────────────────

func clamp(val, min, max int) int {
	if val < min {
		return min
	}
	if val > max {
		return max
	}
	return val
}

// ellipsis is the one-cell marker every clipped string ends in.
const ellipsis = "…"

func truncate(s string, max int) string {
	// A width budget computed from the terminal size (termWidth-6 and friends)
	// goes negative on a very small window. Clamping here rather than at every
	// call site is what keeps the whole render path from panicking on a slice
	// bound when the user drags the terminal down to a few columns.
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + ellipsis
}

// truncateStyled is truncate for a string that has already been through a
// lipgloss .Render. truncate counts runes, and an SGR sequence is a dozen of
// them, so it cuts *inside* the escape: the terminal then reads the ellipsis
// marker as more sequence parameters and swallows it, printing whatever falls
// out the other end, and the style never terminates — which is what leaked
// into the panel border and broke the frame. Measuring and cutting through
// ansi keeps every sequence whole.
func truncateStyled(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= max {
		return s
	}
	return ansi.Truncate(s, max, ellipsis)
}

// shortID returns the first 8 chars of a task ID — the same prefix the CLI
// shows and accepts in commands like `tjek show <prefix>`.
func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// padRight/padLeft/padCenter all clamp a negative width to zero for the same
// reason truncate does: the widths are computed from the terminal size, and a
// few-column window drives them below zero — where the rune slice below would
// panic and strings.Repeat would too.
func padRight(s string, width int) string {
	if width < 0 {
		width = 0
	}
	r := []rune(s)
	if len(r) >= width {
		return string(r[:width])
	}
	return s + strings.Repeat(" ", width-len(r))
}

func padLeft(s string, width int) string {
	if width < 0 {
		width = 0
	}
	r := []rune(s)
	if len(r) >= width {
		return string(r[:width])
	}
	return strings.Repeat(" ", width-len(r)) + s
}

func padCenter(s string, width int) string {
	if width < 0 {
		width = 0
	}
	r := []rune(s)
	if len(r) >= width {
		return string(r[:width])
	}
	pad := width - len(r)
	left := pad / 2
	right := pad - left
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", right)
}

func wrapText(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	runes := []rune(s)
	lines := make([]string, 0, (len(runes)/width)+1)
	for len(runes) > 0 {
		if len(runes) <= width {
			lines = append(lines, string(runes))
			break
		}
		cutAt := width
		minCut := width / 2
		if minCut < 1 {
			minCut = 1
		}
		for i := width; i > minCut; i-- {
			if runes[i] == ' ' {
				cutAt = i
				break
			}
		}
		lines = append(lines, string(runes[:cutAt]))
		runes = runes[cutAt:]
		for len(runes) > 0 && runes[0] == ' ' {
			runes = runes[1:]
		}
	}
	return lines
}

// clampLines caps a wrapped block at max lines, marking the last one with the
// ellipsis so a cut block can be told from one that simply ends there — the
// same contract truncate and truncateLines keep for a cut line. max <= 0
// returns nothing.
func clampLines(lines []string, max int) []string {
	if max <= 0 {
		return nil
	}
	if len(lines) <= max {
		return lines
	}
	out := append([]string(nil), lines[:max]...)
	// Mark the cut in place of the last rune rather than after it: every line
	// here is already wrapped to the pane width, so appending would be the one
	// column that breaks the no-wrap contract.
	if last := []rune(out[max-1]); len(last) > 0 {
		out[max-1] = string(last[:len(last)-1]) + ellipsis
	}
	return out
}

func commentLineCount(text string, available int) int {
	n := len([]rune(text))
	if n == 0 {
		return 1
	}
	if lines := (n + available - 1) / available; lines > 1 {
		return lines
	}
	return 1
}

func renderTagsPart(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.Grow(len(tags) * 12)
	for _, tag := range tags {
		sb.WriteString(tagStyle.Render("⟨#"+tag+"⟩") + " ")
	}
	return sb.String()
}

// rowTagWords spells a row's tags the way the Tags column shows them: "#tag",
// with no chip brackets. A list row is the dense view, where the two bracket
// cells per tag are better spent on another tag; the detail pane and the
// calendar, which give each tag room of its own, keep the ⟨#tag⟩ chips.
func rowTagWords(tags []string) []string {
	words := make([]string, len(tags), len(tags)+1)
	for i, tag := range tags {
		words[i] = "#" + tag
	}
	return words
}

// rowTagsWidth is the width of a row's tags as rowTagWords spells them, one
// space apart, without the Tags cell's leading gap.
func rowTagsWidth(tags []string) int {
	if len(tags) == 0 {
		return 0
	}
	w := len(tags) - 1
	for _, tag := range tags {
		w += 1 + len([]rune(tag))
	}
	return w
}

// renderTaskTagCells draws a row's Tags cell: a leading gap, then words — tags,
// a clipped tag, a "+N" count — one space apart in the tag colour. On a
// selected row the selection background runs through the gap and the spaces
// too, so the highlight does not break where the tags start.
func renderTaskTagCells(words []string, selected bool) string {
	if len(words) == 0 {
		return ""
	}
	if selected {
		return taskTagSelectedRowStyle.Render(" " + strings.Join(words, " "))
	}
	return " " + tagStyle.Render(strings.Join(words, " "))
}

// selectedRowTail fills the rest of a selected row with the selection
// background. A row's columns stop where its content does — a task with no
// tags ends at the Project column, a subtask a few characters after its title —
// so without this the highlight ends partway across the pane, which reads as a
// stray block rather than "this is the row you are on". drawn is the display
// width the row has already emitted; contentW is the pane's inner width
// (termWidth-8, the same budget the header pads itself to).
func selectedRowTail(st fastStyle, drawn, contentW int) string {
	pad := contentW - drawn
	if pad <= 0 {
		return ""
	}
	return st.render(strings.Repeat(" ", pad))
}

// renderTaskTagsClipped draws as many of a row's tags as fit in avail display
// cells and closes with a "+N" count for the rest. It returns the styled cell
// and the width it drew. Callers reach it through model.renderRowTags, which
// serves the whole-set case from the render cache and only falls through to
// here when tags actually have to be dropped. Tags are dropped from the end
// because the tag list is already sorted on the task, so the order is stable
// frame to frame. The first tag that does not fit whole is clipped into the
// room left rather than dropped, as long as a couple of its letters show:
// "#bug #rele… +1" still names the tag, where "#bug +2" says only that there
// are more.
func renderTaskTagsClipped(tags []string, avail int, selected bool) (string, int) {
	// k counts down from the full set so the function is total: given room
	// for every tag it draws every tag, and a caller that has not pre-checked
	// the fit still gets the right answer.
	for k := len(tags); k >= 0; k-- {
		words := rowTagWords(tags[:k])
		if k == len(tags) {
			if k > 0 && tagCellWidth(words) <= avail {
				return renderTaskTagCells(words, selected), tagCellWidth(words)
			}
			continue
		}
		var after []string
		if rest := len(tags) - k - 1; rest > 0 {
			after = []string{"+" + strconv.Itoa(rest)}
		}
		if room := avail - tagCellWidth(append(append(words[:k:k], ""), after...)); room >= tagClipMinW {
			words = append(append(words, truncate("#"+tags[k], room)), after...)
			return renderTaskTagCells(words, selected), tagCellWidth(words)
		}
		words = append(words, "+"+strconv.Itoa(len(tags)-k))
		if w := tagCellWidth(words); w <= avail {
			return renderTaskTagCells(words, selected), w
		}
	}
	return "", 0
}

// tagCellWidth is the width renderTaskTagCells draws words at: the leading gap
// and the words one space apart.
func tagCellWidth(words []string) int {
	w := len(words)
	for _, s := range words {
		w += len([]rune(s))
	}
	return w
}

// listCols decides which columns of the task/history list fit at the current
// terminal width. Columns are dropped least-important-first as the window
// narrows so list lines never wrap inside the panel.
//
// showSize is active-only (history doesn't expose size); showLast carries the
// Score for active rows and the Completed date for history rows.
// showTags is true when at least one visible row has tags (tagsMax > 0); when
// false the "Tags" header label is suppressed so it never appears on a list
// where every row would show a blank tags cell.
type listCols struct {
	titleW      int
	projectW    int // actual width of the Project column (0 when showProject=false)
	dueW        int // width of the Due column (sized to content on the active list)
	lastW       int // Score (active) or Completed (history)
	sizeW       int // Size column: one letter, but its header is four
	showSize    bool
	showDue     bool
	showLast    bool // Score (active) or Completed (history)
	showProject bool
	showTags    bool // true when at least one visible row has tags
}

// hugColW sizes one list column: the wider of its header label and its widest
// value, plus a single column gap. That is the whole rule — a column costs what
// it has to show and not a cell more, so the space it does not need goes to the
// title and the tags, which are the columns that can actually use it. Sizing a
// column for anything else (a fixed constant, an asymmetric pad meant to add up
// to some rhythm) strands blanks on every row whose value is shorter.
func hugColW(valueW int, header string) int {
	w := valueW
	if h := len([]rune(header)); h > w {
		w = h
	}
	return w + listColGap
}

// dueColMax returns the widest rendered due value (formatDueShort) across the
// given top-level tasks, used to size the active list's Due column to exactly
// its content. Computed per frame rather than cached because the rendered width
// depends on the current time — a task crossing a day boundary ("1d"→"today")
// or the 28-day cutoff ("28d"→"15-06-26") changes its width — so the column
// must track the exact strings the rows draw this frame and never clip them.
func dueColMax(tasks []todo.Todo, now time.Time) int {
	max := 0
	for i := range tasks {
		if tasks[i].DueDate.IsZero() {
			continue
		}
		if w := len([]rune(formatDueShort(tasks[i].DueDate, now))); w > max {
			max = w
		}
	}
	return max
}

// taskListCols decides which columns of the task/history list fit at the
// current terminal width. hasDue must be true when at least one visible row
// carries a non-zero due date; when it is false the Due column is omitted
// entirely so space is not wasted on an always-empty column. dueMax is the
// rune-count of the widest rendered due value, used to size the Due column to
// its content (see dueColMax). widestProject is the rune-count of the longest
// visible project name; when it is 0 the Project column collapses entirely (no
// header label, no reserved space).
func taskListCols(termWidth int, isHistory bool, contentMax, tagsMax int, hasDue bool, dueMax, widestProject int) listCols {
	inner := termWidth - 8 // panel content width (margin + border + padding)
	const fixed = 6        // cursor + checkbox + fold icon
	c := listCols{showDue: hasDue, showLast: true, showTags: tagsMax > 0}
	projectWant := 0
	if !isHistory {
		c.showSize = true
		if widestProject > 0 {
			c.showProject = true
			// Start at the compact baseline for ordinary layouts. After the fixed
			// columns and title have claimed what they need, genuine spare width is
			// offered back to Project below so wide terminals reveal longer names.
			projectWant = hugColW(widestProject, listHeader("Project"))
			c.projectW = projectWant
			if c.projectW > projectColCompactW {
				c.projectW = projectColCompactW
			}
		}
	}

	// Title column fits its longest entry (+ the shared column gap), floored to
	// the header label so it never truncates, capped by the shared responsive
	// width.
	// The floor carries the column gap, not just the label. Without it a list
	// whose longest title is shorter than its own header sized the column to
	// exactly the header, and the header ran straight into the next one:
	// "Active tasksScore". Real titles usually dwarf the label, which is why
	// this only showed on a short list — the Projects tab already floors this
	// way for the same reason, its names being as short as "Project".
	floor := len([]rune(tr("Active tasks"))) + listColGap
	if isHistory {
		floor = len([]rune(tr("Completed tasks"))) + listColGap
	}
	c.titleW = contentFitWidth(termWidth, contentMax, listColGap, floor)

	// Score holds a percentage, so its widest value is "100%"; the header is
	// wider than that, which is what actually sizes the column.
	lastW := hugColW(scoreValW, listHeader("Score"))
	sizeW := hugColW(1, listHeader("Size"))
	// The active list shows short relative due values ("2d", "today"), so hug the
	// Due column to its widest entry: a list with nothing but "3d" values does
	// not strand a full-date-wide empty column. Capped at dueValMaxW, the
	// full-date worst case, so one far-off task cannot widen it further. History
	// always shows absolute dates, so it keeps the fixed 12-wide column that also
	// matches its Completed column.
	dueW := hugColW(min(dueMax, dueValMaxW), listHeader("Due"))
	if isHistory {
		lastW = 12
		dueW = 12
	}
	c.dueW = dueW
	c.lastW = lastW
	c.sizeW = sizeW
	colsW := func() int {
		w := 0
		if c.showSize {
			w += c.sizeW
		}
		if c.showDue {
			w += dueW
		}
		if c.showLast {
			w += lastW
		}
		if c.showProject {
			w += c.projectW
		}
		return w
	}

	// Drop order on narrow terminals:
	//   active:  Project → Size → Score → Due  (Project drops first since it
	//            shows on most rows as a single short word; keep Due longest
	//            — it's the hard fact)
	//   history: Due  → Completed     (Size and Project never shown)
	drop := []*bool{&c.showProject, &c.showSize, &c.showLast, &c.showDue}
	if isHistory {
		drop = []*bool{&c.showDue, &c.showLast}
	}
	// A row that has tags keeps enough room to say so. Three cells buy " +N",
	// which is the difference between "this task has two more tags" and "this
	// task has no tags" — a distinction the row cannot make in fewer. It is
	// only a floor: the tags themselves still take whatever is left over.
	tagsMin := 0
	if c.showTags {
		tagsMin = tagsOverflowMinW
	}
	for _, d := range drop {
		if inner-fixed-c.titleW-colsW()-tagsMin >= 0 {
			break
		}
		*d = false
	}
	if !c.showProject {
		c.projectW = 0
	}

	// The flat name-column cap (nameColWidth) keeps the title sane on the other
	// list tabs, but on a wide terminal it can clip a long title while empty
	// space sits to the right of the fixed columns. Grow the title to absorb
	// that slack — but never past what the longest entry actually needs, and
	// leave room for the trailing tags column (a leading space + the widest
	// row's tags) so growing the title can't push tags off the right edge.
	// Reserve room for the tags before growing the title into spare width — but
	// only a share of it. Reserving the widest tag row's full width meant one
	// tag-heavy task clipped every title in the list, and the reserved cells
	// then went unused anyway, because the tags still did not fit and
	// collapsed to a marker. Capping the reserve and letting the cell degrade
	// to "#bug +2" spends the width on whichever column can use it.
	tagsReserve := 0
	if tagsMax > 0 {
		tagsReserve = 1 + tagsMax
		if capW := inner * tagsReservePct / 100; tagsReserve > capW {
			tagsReserve = capW
		}
		if tagsReserve < tagsOverflowMinW {
			tagsReserve = tagsOverflowMinW
		}
	}
	if want := contentMax + listColGap; c.titleW < want {
		if spare := inner - fixed - c.titleW - colsW() - tagsReserve; spare > 0 {
			grow := want - c.titleW
			if grow > spare {
				grow = spare
			}
			c.titleW += grow
		}
	}

	// Once task titles and tags have enough room, let a visible Project
	// column consume the remaining slack up to its actual content need. This is
	// deliberately after title growth: it removes otherwise-empty space without
	// making task titles truncate sooner merely to widen a secondary column.
	if c.showProject && c.projectW < projectWant {
		spare := inner - fixed - c.titleW - colsW() - tagsReserve
		if spare > 0 {
			grow := projectWant - c.projectW
			if grow > spare {
				grow = spare
			}
			c.projectW += grow
		}
	}

	return c
}

// listPosLabel formats the "cursor/total" scroll-position indicator for a list
// header, clamping the cursor into range. Returns "" for an empty list.
func listPosLabel(cursor, total int) string {
	if total <= 0 {
		return ""
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= total {
		cursor = total - 1
	}
	return fmt.Sprintf("%d/%d", cursor+1, total)
}

// posLabel, when non-empty (e.g. "3/47"), is drawn right-aligned on the header
// as a scroll-position indicator; pass "" to omit it.
func renderListHeader(b *strings.Builder, termWidth int, isHistory bool, c listCols, posLabel string) {
	title := tr("Active tasks")
	if isHistory {
		title = tr("Completed tasks")
	}
	renderListHeaderTitled(b, termWidth, isHistory, c, posLabel, title)
}

// renderListHeaderTitled is renderListHeader with the title column's label
// given, for lists that are neither all open nor all done (a tag or project
// drill-in holds both).
func renderListHeaderTitled(b *strings.Builder, termWidth int, isHistory bool, c listCols, posLabel, title string) {
	dueW := c.dueW
	sizeLabel := padRight(listHeader("Size"), c.sizeW)
	// Score and Due are right-aligned value fields on the active list (see
	// listColGap), so their labels sit over the field rather than at the
	// column's left edge — otherwise the header names a column whose values
	// end five cells further right.
	dueLabel := padRight(padLeft(listHeader("Due"), dueW-listColGap), dueW)
	lastLabel := padRight(padLeft(listHeader("Score"), c.lastW-listColGap), c.lastW)
	// The active-sort cue lives in the panel border title, so column headers
	// stay plain — no >..< decoration to reflow.
	if isHistory {
		// History's dates are all the same width, so its columns stay
		// left-aligned and its labels with them.
		sizeLabel = padCenter(listHeader("Size"), c.sizeW)
		dueLabel = padRight(listHeader("Due"), dueW)
		lastLabel = padRight(tr("Completed"), 12)
	}

	const prefix = "      "
	headerLeft := prefix + padRight(title, c.titleW)
	// Active view: Score sits right after the title. History keeps the
	// historical Due → Completed order so the completion date stays next
	// to the due date.
	if c.showLast && !isHistory {
		headerLeft += lastLabel
	}
	if c.showDue {
		headerLeft += dueLabel
	}
	if c.showSize {
		headerLeft += sizeLabel
	}
	if c.showLast && isHistory {
		headerLeft += lastLabel
	}
	if c.showProject {
		headerLeft += padRight(listHeader("Project"), c.projectW)
	}
	// Row tags are rendered with a leading space (see renderTaskLineWithSet), so
	// the header label needs the same lead-in to line up with the tag content.
	// Only show the "Tags" label when at least one visible row actually has tags
	// (c.showTags), so the header never reserves space for an always-blank column.
	tagsLabel := " " + listHeader("Tags")
	// Reserve the right end for the position indicator (a leading space + the
	// label) before the tags label claims the slack, so a full list can't push
	// the indicator off the edge.
	// Show the indicator only when it fits within the pane; otherwise drop it
	// rather than overflow or get truncated mid-label on a narrow terminal.
	showPos := posLabel != "" && len([]rune(headerLeft))+1+len([]rune(posLabel)) <= termWidth-8
	posW := 0
	if showPos {
		posW = 1 + len([]rune(posLabel))
	}
	padW := termWidth - 8 - len([]rune(headerLeft)) - posW
	if c.showTags {
		// When only the minimal "+N" cell fits there is no room for the word,
		// but the sigil every tag starts with says the same
		// thing in two cells — and a lone "+2" under no heading at all reads
		// as belonging to whichever column happens to precede it.
		if padW < len([]rune(tagsLabel)) {
			tagsLabel = " #"
		}
		if padW >= len([]rune(tagsLabel)) {
			headerLeft += tagsLabel
			padW -= len([]rune(tagsLabel))
		}
	}
	if padW < 0 {
		padW = 0
	}
	line := headerStyle.Render(headerLeft + strings.Repeat(" ", padW))
	if showPos {
		line += " " + pageIndicatorStyle.Render(posLabel)
	}
	b.WriteString(line + "\n")
}

// ── Editor support ────────────────────────────────────────────────────────────

func resolveEditorCmd() string {
	// Memoized on the inputs the answer depends on. A miss walks every PATH
	// entry for up to six candidates, and on Windows each of those is
	// multiplied by PATHEXT — a few thousand stat syscalls when no editor is
	// installed, which is the normal case on a CI runner. initialModel calls
	// this on every construction and the editor launch calls it again, so the
	// uncached version turned a test that builds many models into a five
	// minute one. Keyed rather than sync.Once so a changed $EDITOR (or PATH)
	// is still honoured.
	key := os.Getenv("EDITOR") + "\x00" + os.Getenv("PATH")
	editorCmdMu.Lock()
	defer editorCmdMu.Unlock()
	if path, ok := editorCmdCache[key]; ok {
		return path
	}
	path := lookUpEditorCmd()
	if editorCmdCache == nil {
		editorCmdCache = make(map[string]string, 1)
	}
	editorCmdCache[key] = path
	return path
}

var (
	editorCmdMu    sync.Mutex
	editorCmdCache map[string]string
)

func lookUpEditorCmd() string {
	if editor := os.Getenv("EDITOR"); editor != "" {
		if path, err := exec.LookPath(editor); err == nil {
			return path
		}
	}
	candidates := []string{"hx", "helix", "nvim", "vim", "nano"}
	if runtime.GOOS == "windows" {
		candidates = append(candidates, "notepad")
	}
	for _, candidate := range candidates {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	return ""
}

// editorDraftKey is the sentinel "task ID" for the scratch file that backs the
// ctrl+e editor escape from a comment input, keeping it out of any real
// task's notes file. Task IDs are UUIDs, so it can never collide with one.
const editorDraftKey = "__tjek_input_draft__"

// notesFilePath is the scratch file $EDITOR is handed. It is cache, not data:
// the notes themselves live in the database, and this copy exists only for the
// seconds an editor is open.
func notesFilePath(taskID string) string {
	dir, err := paths.Ensure(paths.Cache)
	if err != nil {
		return ""
	}
	dir = filepath.Join(dir, "notes")
	_ = os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, taskID+".md")
}

func writeNotesFile(taskID, content string) error {
	return writeFileAtomic(notesFilePath(taskID), []byte(content), 0644)
}

func readNotesFile(taskID string) (string, error) {
	data, err := os.ReadFile(notesFilePath(taskID))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

func cleanupNotesFile(taskID string) {
	_ = os.Remove(notesFilePath(taskID))
}

// ── Quick-add parsing ─────────────────────────────────────────────────────────

type parsedTask struct {
	title    string
	tags     []string
	project  string
	dueDate  time.Time
	priority todo.Priority
	size     todo.Size
	// hasPriority/hasSize record whether a p:/s: token was actually present, so
	// a CLI caller can tell "no token" from the Medium default and avoid
	// clobbering a --like clone. The TUI ignores them (it always starts fresh).
	hasPriority bool
	hasSize     bool
	recurrence  string
	// deps holds raw, unresolved refs from dep: tokens (id-prefixes or the
	// `^` last-added shorthand). Resolution needs the live task set, so it
	// happens at the call site, not here.
	deps []string
	// unparsed holds the words that opened with a field prefix (due:, p:, s:,
	// r:, dep:) but whose value the grammar did not take. They stay in the
	// title, and the callers say so: a mistyped token otherwise vanishes into
	// the title without a word.
	unparsed []string
}

func parseQuickAdd(input string) parsedTask {
	result := parsedTask{priority: todo.PriorityMedium, size: todo.SizeMedium}
	words := strings.Fields(input)
	var titleWords []string

	for _, word := range words {
		// Lower once, then fold a localized field prefix back to its English
		// form (frist: → due:) so the grammar below knows one spelling of each.
		// `word` keeps its original case for the branches that carry a value
		// through — tags, projects, and the title fallback.
		lower := canonicalInputToken(strings.ToLower(word))
		switch {
		case strings.HasPrefix(word, "#"):
			if tag := todo.NormalizeTag(word); tag != "" {
				result.tags = append(result.tags, tag)
			}
		case strings.HasPrefix(lower, "due:"):
			if d, err := parseDueDate(strings.TrimPrefix(lower, "due:")); err == nil {
				result.dueDate = d
			} else {
				titleWords = append(titleWords, word)
				result.unparsed = append(result.unparsed, word)
			}
		case strings.HasPrefix(word, "@"):
			if proj := strings.TrimPrefix(word, "@"); proj != "" {
				result.project = proj
			}
		case strings.HasPrefix(lower, "p:"):
			switch canonicalInputWord(strings.TrimPrefix(lower, "p:")) {
			case "high", "h":
				result.priority, result.hasPriority = todo.PriorityHigh, true
			case "medium", "med", "m":
				result.priority, result.hasPriority = todo.PriorityMedium, true
			case "low", "l":
				result.priority, result.hasPriority = todo.PriorityLow, true
			default:
				titleWords = append(titleWords, word)
				result.unparsed = append(result.unparsed, word)
			}
		case strings.HasPrefix(lower, "size:") || strings.HasPrefix(lower, "s:"):
			spec := strings.TrimPrefix(strings.TrimPrefix(lower, "size:"), "s:")
			switch canonicalInputWord(spec) {
			case "s", "small":
				result.size, result.hasSize = todo.SizeSmall, true
			case "m", "med", "medium":
				result.size, result.hasSize = todo.SizeMedium, true
			case "l", "large":
				result.size, result.hasSize = todo.SizeLarge, true
			default:
				titleWords = append(titleWords, word)
				result.unparsed = append(result.unparsed, word)
			}
		case strings.HasPrefix(lower, "r:") || strings.HasPrefix(lower, "recur:"):
			spec := strings.TrimPrefix(strings.TrimPrefix(lower, "recur:"), "r:")
			// The canonical rule stays English on disk; only the word typed
			// here is localized, so ParseRecurrence keeps its locale-free
			// vocabulary (it lives in the domain package).
			if canonical, ok := todo.ParseRecurrence(canonicalInputWord(spec)); ok && canonical != "" {
				result.recurrence = canonical
			} else {
				titleWords = append(titleWords, word)
				result.unparsed = append(result.unparsed, word)
			}
		case strings.HasPrefix(lower, "dep:"):
			// Whitespace-delimited, so only id-prefix refs (or ^) fit here —
			// title-substring refs have spaces and stay a CLI/edit affair.
			// Read off `lower`: the prefix may have been a localized one, and
			// the refs this accepts (a hex id prefix, or ^) are case-free.
			if ref := lower[len("dep:"):]; ref != "" {
				result.deps = append(result.deps, ref)
			} else {
				titleWords = append(titleWords, word)
				result.unparsed = append(result.unparsed, word)
			}
		default:
			titleWords = append(titleWords, word)
		}
	}

	result.title = strings.Join(titleWords, " ")
	return result
}

// ── Time formatting ───────────────────────────────────────────────────────────

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// formatStartDate renders a start date for display: the day, plus the time of
// day when one is recorded (see todo.SetStartDate — started tasks carry a
// real time, so cycle-time reads carry that precision). Legacy midnight values
// show as a bare date.
func formatStartDate(t time.Time) string {
	if t.Equal(startOfDay(t)) {
		return t.Format("02-01-06")
	}
	return t.Format("02-01-06 15:04")
}

// formatDueShort renders a due date for the tasks-list column as its distance
// in calendar days — "today", "3d", "-2d" (overdue) — because "how soon" is
// the question the list answers. Beyond four weeks a day count reads worse
// than a date, so it falls back to the absolute form: dd-mm for dates in the
// current year (saving space), or dd-mm-yy for dates in a different year.
// Detail views keep the full absolute form everywhere: it round-trips through
// the date editor.
func formatDueShort(due, now time.Time) string {
	// Round rather than truncate: local midnights straddling a DST switch are
	// 23 or 25 hours apart and would otherwise land on the wrong day.
	days := int(math.Round(startOfDay(due).Sub(startOfDay(now)).Hours() / 24))
	switch {
	case days == 0:
		return tr("today")
	case days < -28 || days > 28:
		if due.Year() == now.Year() {
			return due.Format("02-01")
		}
		return due.Format("02-01-06")
	default:
		return fmt.Sprintf("%dd", days)
	}
}

// startsCell is what a task that starts on a later day shows in the Score
// column: the day it can be picked up, as a weekday within the week and as
// dd-mm beyond it. It sorts below the startable work, so a percentage there
// would contradict its place in the list.
func startsCell(start, now time.Time) string {
	days := int(math.Round(startOfDay(start).Sub(startOfDay(now)).Hours() / 24))
	if days >= 1 && days <= 6 {
		return localizedWeekdayShort(start.Weekday())
	}
	return start.Format("02-01")
}

// startsLong is startsCell for a row with room: "starts Fri 02-10-26".
func startsLong(start time.Time) string {
	return fmt.Sprintf(tr("starts %s"), localizedWeekdayShort(start.Weekday())+" "+start.Format("02-01-06"))
}

// formatDurationLive renders a running duration with seconds, for the
// live timer indicator in the footer.
func formatDurationLive(d time.Duration) string {
	s := int(d.Seconds())
	if s < 0 {
		s = 0
	}
	if s < 3600 {
		return fmt.Sprintf("%dm %02ds", s/60, s%60)
	}
	return fmt.Sprintf("%dh %02dm %02ds", s/3600, (s%3600)/60, s%60)
}

// formatDurationCompact renders without spaces for narrow columns: 48m, 1h39m, 12h.
func formatDurationCompact(d time.Duration) string {
	mins := int(d.Minutes())
	if mins < 60 {
		return fmt.Sprintf("%dm", mins)
	}
	if mins%60 == 0 {
		return fmt.Sprintf("%dh", mins/60)
	}
	return fmt.Sprintf("%dh%02dm", mins/60, mins%60)
}

// parseEntryEdit parses a time-entry edit: "HH:MM-HH:MM", "HH:MM-now"
// (keeps the entry running), or a bare duration ("45m", "1h30m", "2h").
// Clock times are interpreted on the entry's original start day; an end
// time before the start is taken to cross midnight.
func parseEntryEdit(input string, oldStart time.Time, running bool) (time.Time, time.Time, error) {
	s := strings.TrimSpace(strings.ToLower(input))
	if s == "" {
		return time.Time{}, time.Time{}, fmt.Errorf("empty input")
	}
	if strings.Contains(s, "-") {
		parts := strings.SplitN(s, "-", 2)
		start, err := parseClockOn(strings.TrimSpace(parts[0]), oldStart)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		endStr := strings.TrimSpace(parts[1])
		if endStr == "now" {
			if !running {
				return time.Time{}, time.Time{}, fmt.Errorf("'now' is only valid for a running entry")
			}
			return start, time.Time{}, nil
		}
		stop, err := parseClockOn(endStr, oldStart)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		if !stop.After(start) {
			stop = stop.AddDate(0, 0, 1) // crosses midnight
		}
		return start, stop, nil
	}
	d, err := time.ParseDuration(strings.ReplaceAll(s, " ", ""))
	if err != nil || d <= 0 {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid input '%s' (use HH:MM-HH:MM or 45m, 1h30m)", input)
	}
	return oldStart, oldStart.Add(d), nil
}

// parseManualEntry resolves user input for a backfilled time entry: a bare
// duration ("45m") becomes [now-d, now] — "I just spent 45m on this" almost
// always means it ends now, not starts now — while a clock range
// ("10:00-11:30") is taken literally on today. Shared by the TUI's
// modeAddTimeEntry and `tjek log` so the two surfaces can't drift.
func parseManualEntry(input string, now time.Time) (start, stop time.Time, err error) {
	start, stop, err = parseEntryEdit(input, now, false)
	if err != nil {
		return start, stop, err
	}
	if !strings.Contains(input, "-") {
		d := stop.Sub(start)
		start = now.Add(-d)
		stop = now
	}
	return start, stop, nil
}

func parseClockOn(s string, day time.Time) (time.Time, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid time '%s' (use HH:MM)", s)
	}
	return time.Date(day.Year(), day.Month(), day.Day(), t.Hour(), t.Minute(), 0, 0, day.Location()), nil
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	hours := int(d.Hours())
	mins := int(d.Minutes()) % 60
	if hours < 24 {
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	days := hours / 24
	hours = hours % 24
	return fmt.Sprintf("%dd %dh", days, hours)
}

// ── Builder pool ──────────────────────────────────────────────────────────────

var builderPool = sync.Pool{
	New: func() interface{} {
		b := &strings.Builder{}
		b.Grow(2048)
		return b
	},
}

func getBuilder() *strings.Builder {
	b := builderPool.Get().(*strings.Builder)
	b.Reset()
	return b
}

func putBuilder(b *strings.Builder) {
	builderPool.Put(b)
}

// ── Self-update ───────────────────────────────────────────────────────────────

// isHomebrewCellarPath reports whether path belongs to Tjek's Homebrew keg.
// Homebrew exposes the command through a symlink, so callers must resolve
// symlinks before checking it.
func isHomebrewCellarPath(path string) bool {
	path = filepath.ToSlash(filepath.Clean(path))
	return strings.Contains(path, "/Cellar/tjek/")
}

// packageManagerFor names the tool that owns the binary at path, or "" when the
// binary looks self-installed. Writing over a package-managed file is undone by
// that manager's next upgrade and fails its integrity check (`pacman -Qkk`,
// `scoop status`) until then, so self-update refuses and points at the owner
// instead. /usr/bin counts because the FHS reserves it for the distribution's
// packages — a hand-installed binary belongs in /usr/local/bin or ~/.local/bin,
// and both of those stay self-updatable.
func packageManagerFor(path string) string {
	// Normalise separators by hand: filepath.ToSlash is a no-op off Windows,
	// so a Windows path examined anywhere else (a test, a cross-platform
	// check) would keep its backslashes and match nothing.
	path = strings.ReplaceAll(filepath.Clean(path), `\`, "/")
	switch {
	case isHomebrewCellarPath(path):
		return "brew update && brew upgrade tjek"
	case strings.Contains(strings.ToLower(path), "/scoop/apps/"):
		return "scoop update tjek"
	case strings.HasPrefix(path, "/usr/bin/"):
		return "your distribution's package manager"
	}
	return ""
}

// runningPackageManager asks packageManagerFor about this process's own binary,
// resolving symlinks first — every one of those layouts is reached through one.
func runningPackageManager() string {
	execPath, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(execPath); err == nil {
		execPath = resolved
	}
	return packageManagerFor(execPath)
}

// selfUpdateAsset names the release asset for a platform. The names are
// load-bearing in both directions: the release workflow attaches exactly these,
// and an already-installed binary looks for the name *it* was built to expect —
// so "tjek" and "tjek.exe" can never be renamed, and a new platform gets a new
// name rather than a redefinition. Architecture is part of the lookup because
// there are now two Linux builds; handing an arm64 machine the amd64 asset would
// install a binary that cannot run.
func selfUpdateAsset(goos, goarch string) (string, error) {
	switch goos {
	case "linux":
		switch goarch {
		case "amd64":
			return "tjek", nil
		case "arm64":
			return "tjek-linux-arm64", nil
		}
		return "", fmt.Errorf("no release build for linux/%s ; install from source with `go install github.com/Morteningemann86/xtratjek@latest`", goarch)
	case "windows":
		if goarch == "amd64" {
			return "tjek.exe", nil
		}
		return "", fmt.Errorf("no release build for windows/%s ; install from source with `go install github.com/Morteningemann86/xtratjek@latest`", goarch)
	case "darwin":
		return "", fmt.Errorf("macOS updates are distributed via Homebrew; run `brew install iliorn/tap/tjek`")
	default:
		return "", fmt.Errorf("self-update is not available for %s", goos)
	}
}

func selfUpdate() error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not determine executable path: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("could not resolve executable path: %w", err)
	}
	if hint := packageManagerFor(execPath); hint != "" {
		return fmt.Errorf("this install is package-managed; update it with `%s`", hint)
	}

	assetName, err := selfUpdateAsset(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	// Stage the download in a private temp dir, not the shared os.TempDir():
	// a fixed, predictable path like /tmp/tjek is writable by any local user,
	// who could swap the file between the download and the install below.
	stageDir, err := os.MkdirTemp("", "tjek-update-")
	if err != nil {
		return fmt.Errorf("could not create staging dir: %w", err)
	}
	defer os.RemoveAll(stageDir)
	tmpFile := filepath.Join(stageDir, assetName)

	info, err := fetchLatestRelease()
	if err != nil {
		return err
	}
	if err := downloadVerifiedAsset(info, assetName, tmpFile); err != nil {
		return err
	}

	if runtime.GOOS == "windows" {
		// Windows forbids overwriting a running executable, but allows
		// renaming it. Move it aside, then copy the new binary into place.
		oldPath := execPath + ".old"
		_ = os.Remove(oldPath)
		if err := os.Rename(execPath, oldPath); err != nil {
			return fmt.Errorf("could not move old binary aside: %w", err)
		}
		if err := copyFile(tmpFile, execPath); err != nil {
			_ = os.Rename(oldPath, execPath) // restore on failure
			return fmt.Errorf("could not install new binary: %w", err)
		}
		return nil
	}

	// Unix refuses to write into a running executable (ETXTBSY). Copy the
	// new binary next to the old one, then rename over it — rename is
	// atomic and allowed while the process is running.
	newPath := execPath + ".new"
	if err := copyFile(tmpFile, newPath); err != nil {
		return fmt.Errorf("could not stage new binary (check permissions): %w", err)
	}
	if err := os.Rename(newPath, execPath); err != nil {
		_ = os.Remove(newPath)
		return fmt.Errorf("could not replace binary: %w", err)
	}
	return nil
}

func copyFile(srcPath, dstPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	return err
}

// ── Release lookup ───────────────────────────────────────────────────────────
//
// Both the update check and the download talk to the GitHub REST API directly
// over stdlib net/http, so updating needs no second tool installed. The
// repository is public, so the endpoint needs no authentication.

// releaseAPIBase is the API root. A variable rather than a constant so tests can
// point it at an httptest server; nothing else reassigns it.
var releaseAPIBase = "https://api.github.com"

// This fork intentionally diverges from upstream (iliorn/tjek) rather than
// tracking it, so self-update must point at this fork's own releases —
// pointing it at upstream would offer to overwrite a custom build (Meetings,
// the ffmpeg checks, …) with the original project's binary. There are no
// releases here yet; self-update reports "no releases found" until one is
// tagged, the same as any repo that hasn't cut one.
const releaseRepo = "Morteningemann86/xtratjek"

// releaseAsset is one downloadable file attached to a release.
type releaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type releaseInfo struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

// fetchLatestRelease reads the newest release's tag and asset list.
func fetchLatestRelease() (releaseInfo, error) {
	url := releaseAPIBase + "/repos/" + releaseRepo + "/releases/latest"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return releaseInfo{}, err
	}
	// The documented Accept header, and a User-Agent because the API rejects
	// requests without one.
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "tjek/"+appVersion)

	resp, err := (&http.Client{Timeout: releaseAPITimeout}).Do(req)
	if err != nil {
		return releaseInfo{}, fmt.Errorf("could not reach github.com: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return releaseInfo{}, releaseHTTPError(resp)
	}
	// Cap the read: a hostile or broken endpoint shouldn't be able to make the
	// app allocate without bound.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxReleaseJSONBytes))
	if err != nil {
		return releaseInfo{}, fmt.Errorf("could not read the release info: %w", err)
	}
	var info releaseInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return releaseInfo{}, fmt.Errorf("could not parse the release info: %w", err)
	}
	if info.TagName == "" {
		return releaseInfo{}, fmt.Errorf("the latest release has no tag name")
	}
	return info, nil
}

// releaseHTTPError turns a non-200 into something a user can act on. Rate
// limiting is the one an unauthenticated caller actually hits (60 requests an
// hour per IP), so it gets its own wording.
func releaseHTTPError(resp *http.Response) error {
	switch {
	case resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0":
		return fmt.Errorf("GitHub rate limit reached; try again later")
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("no releases found for %s", releaseRepo)
	default:
		return fmt.Errorf("github.com returned %s", resp.Status)
	}
}

// latestRelease returns the tag name of the most recent GitHub release.
func latestRelease() (string, error) {
	info, err := fetchLatestRelease()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(info.TagName), nil
}

// findReleaseAsset picks the asset with the exact name the platform needs. The
// names are load-bearing (see the release workflow), so this is an exact match
// rather than a guess.
func findReleaseAsset(info releaseInfo, name string) (releaseAsset, error) {
	for _, a := range info.Assets {
		if a.Name == name {
			if a.URL == "" {
				return releaseAsset{}, fmt.Errorf("release asset %q has no download URL", name)
			}
			return a, nil
		}
	}
	return releaseAsset{}, fmt.Errorf("release %s has no %q asset", info.TagName, name)
}

// downloadReleaseAsset streams the asset to dst. It writes through a temp file
// the caller owns, so a truncated download never lands on the binary.
// downloadReleaseAsset writes an asset to dst and returns the SHA-256 of the
// bytes it actually wrote, hashed as they stream past so the check can never
// read a different file than the one that was installed.
func downloadReleaseAsset(asset releaseAsset, dst string) (digest string, err error) {
	// The download URL arrives inside the API response rather than being built
	// here, so it is checked before it is followed — and again on every
	// redirect, which is the hop a pre-flight check alone would miss.
	if err := checkAssetURL(asset.URL); err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodGet, asset.URL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "tjek/"+appVersion)

	client := &http.Client{
		Timeout: releaseDownloadTimeout,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			return checkAssetURL(r.URL.String())
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: github.com returned %s", resp.Status)
	}

	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return "", err
	}
	sum := sha256.New()
	written, err := io.Copy(io.MultiWriter(f, sum), io.LimitReader(resp.Body, maxReleaseAssetBytes))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	if written == 0 {
		return "", fmt.Errorf("download failed: the asset was empty")
	}
	if written == maxReleaseAssetBytes {
		return "", fmt.Errorf("download failed: the asset is larger than the %d MB cap",
			maxReleaseAssetBytes/(1024*1024))
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// checkAssetURL refuses a release asset that does not come from GitHub over
// TLS. It is defence in depth rather than the main line: the asset list is read
// over HTTPS from api.github.com, so redirecting a download elsewhere already
// requires forging that response. But the URL is attacker-shaped data — it
// arrives in a JSON body — and following it unchecked is the kind of thing that
// only needs one bad day at the other end.
//
// The check is on the registrable domain, not an exact host list. GitHub has
// moved release assets between hostnames before (objects. → release-assets.),
// and an exact list would turn that into a broken update path for every
// already-installed binary — a self-inflicted outage worse than the risk.
func checkAssetURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("refusing to download from an unparseable URL %q: %w", raw, err)
	}
	// A test server stands in for GitHub, on http and a loopback port. Deferring
	// to whatever releaseAPIBase was pointed at covers it without a second code
	// path, and outside tests that variable is the real API over TLS — which no
	// asset URL's host matches, so real downloads still go through both checks
	// below.
	if base, berr := url.Parse(releaseAPIBase); berr == nil && base.Host != "" && base.Host == u.Host {
		return nil
	}
	if u.Scheme != "https" {
		return fmt.Errorf("refusing to download a release asset over %q: https only", u.Scheme)
	}
	host := strings.ToLower(u.Hostname())
	for _, domain := range []string{"github.com", "githubusercontent.com"} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return nil
		}
	}
	return fmt.Errorf("refusing to download a release asset from %q: only github.com and githubusercontent.com are trusted", u.Host)
}

// ── Update integrity ─────────────────────────────────────────────────────────
//
// Every release carries a SHA256SUMS asset listing the checksum of each binary.
// The update path refuses to install anything it has not checked against it.
// This is an integrity check, not a signature: it proves the bytes that landed
// on disk are the bytes the release lists, which covers a truncated download, a
// proxy rewriting the response and a mirror serving something stale. It does
// not defend against someone who can edit the release itself — that needs a
// signature and a key to check it against, which is a separate decision about
// key distribution.

const sha256SumsAsset = "SHA256SUMS"

// checksumFor pulls one asset's expected digest out of a sha256sum(1) listing:
// lines of "<hex>  <name>", where GNU coreutils marks binary mode with a "*"
// before the name.
func checksumFor(sums []byte, assetName string) (string, error) {
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name != assetName {
			continue
		}
		digest := strings.ToLower(fields[0])
		if len(digest) != sha256.Size*2 {
			return "", fmt.Errorf("%s lists a malformed checksum for %s", sha256SumsAsset, assetName)
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return "", fmt.Errorf("%s lists a malformed checksum for %s", sha256SumsAsset, assetName)
		}
		return digest, nil
	}
	return "", fmt.Errorf("%s does not list %s", sha256SumsAsset, assetName)
}

// downloadVerifiedAsset downloads assetName into dst and installs nothing
// unless its checksum matches the release's SHA256SUMS. It fails closed: a
// release without that asset, or without an entry for this platform's binary,
// is not something to install blind.
func downloadVerifiedAsset(info releaseInfo, assetName, dst string) error {
	sumsAsset, err := findReleaseAsset(info, sha256SumsAsset)
	if err != nil {
		return fmt.Errorf("release %s publishes no %s, so the download cannot be verified; "+
			"install it manually from https://github.com/%s/releases", info.TagName, sha256SumsAsset, releaseRepo)
	}
	sumsPath := dst + "." + sha256SumsAsset
	if _, err := downloadReleaseAsset(sumsAsset, sumsPath); err != nil {
		return fmt.Errorf("could not fetch %s: %w", sha256SumsAsset, err)
	}
	sums, err := os.ReadFile(sumsPath)
	if err != nil {
		return fmt.Errorf("could not read %s: %w", sha256SumsAsset, err)
	}
	want, err := checksumFor(sums, assetName)
	if err != nil {
		return err
	}

	asset, err := findReleaseAsset(info, assetName)
	if err != nil {
		return err
	}
	got, err := downloadReleaseAsset(asset, dst)
	if err != nil {
		return err
	}
	if !strings.EqualFold(got, want) {
		// Remove the staged file rather than leave a binary that failed its
		// check sitting on disk for something else to pick up.
		_ = os.Remove(dst)
		return fmt.Errorf("checksum mismatch for %s: the release lists %s but the download hashed to %s; refusing to install",
			assetName, want[:16]+"…", got[:16]+"…")
	}
	return nil
}

// ── Date parsing ─────────────────────────────────────────────────────────────

// parseCompletedAt reads a corrected completion time: any date parseDueDate
// takes, optionally followed by HH:MM, or a bare HH:MM on the day already
// recorded. A date without a time keeps the recorded time of day, since the
// usual correction is "that was yesterday", not "at midnight". A completion
// cannot lie in the future; a kept time that would put today's date there
// falls back to now.
func parseCompletedAt(input string, prev, now time.Time) (time.Time, error) {
	input = strings.TrimSpace(input)
	datePart, clock := input, ""
	if i := strings.LastIndex(input, " "); i >= 0 {
		if _, err := time.Parse("15:04", input[i+1:]); err == nil {
			datePart, clock = strings.TrimSpace(input[:i]), input[i+1:]
		}
	} else if _, err := time.Parse("15:04", input); err == nil {
		datePart, clock = "", input
	}

	prev = prev.In(now.Location())
	day := prev
	if datePart != "" {
		d, err := parseDueDateAt(datePart, now)
		if err != nil {
			return time.Time{}, errors.New(invalidDateMsg())
		}
		day = d
	} else if clock == "" {
		return time.Time{}, errors.New(invalidDateMsg())
	}

	hour, minute := prev.Hour(), prev.Minute()
	if clock != "" {
		c, _ := time.Parse("15:04", clock)
		hour, minute = c.Hour(), c.Minute()
	}
	at := time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, now.Location())
	if at.After(now) {
		if clock == "" && startOfDay(at).Equal(startOfDay(now)) {
			return now, nil
		}
		return time.Time{}, errors.New(tr("Completion can't be in the future"))
	}
	return at, nil
}

// parseDueDate accepts dd-mm-yy, dd-mm-yyyy, and natural language shortcuts —
// in English always, and in the active interface language too (lang_input.go),
// so a Danish screen takes `imorgen` as readily as `tomorrow`.
func parseDueDate(s string) (time.Time, error) {
	return parseDueDateAt(s, time.Now())
}

// parseDueDateAt is parseDueDate with relative words ("yesterday", "+3d",
// weekday names) counted from now.
func parseDueDateAt(s string, now time.Time) (time.Time, error) {
	lower := canonicalInputWord(strings.ToLower(strings.TrimSpace(s)))
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	switch lower {
	case "today":
		return today, nil
	case "tomorrow":
		return today.AddDate(0, 0, 1), nil
	case "yesterday":
		return today.AddDate(0, 0, -1), nil
	case "next week":
		return today.AddDate(0, 0, 7), nil
	case "next month":
		return today.AddDate(0, 1, 0), nil
	}

	if dayName, ok := splitNextPrefix(lower); ok {
		if weekday, ok := parseWeekday(dayName); ok {
			return nextWeekday(today, weekday), nil
		}
	}

	if weekday, ok := parseWeekday(lower); ok {
		return nextWeekday(today, weekday), nil
	}

	// "+3d" counts forward and "-2d" back: the list shows an overdue date as
	// "-2d", so what it prints can be typed back in.
	if (strings.HasPrefix(lower, "+") || strings.HasPrefix(lower, "-")) && len(lower) > 2 {
		unit := lower[len(lower)-1]
		numStr := lower[1 : len(lower)-1]
		if n, ok := parsePositiveInt(numStr); ok && n > 0 {
			if lower[0] == '-' {
				n = -n
			}
			switch unit {
			case 'd':
				return today.AddDate(0, 0, n), nil
			case 'w':
				return today.AddDate(0, 0, n*7), nil
			case 'm':
				return today.AddDate(0, n, 0), nil
			}
		}
	}

	if t, err := time.ParseInLocation("02-01-06", s, now.Location()); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("02-01-2006", s, now.Location()); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid date: use dd-mm-yy, %q, %q, %q, %q, or '+Nd/+Nw/+Nm' (- counts back)",
		inputWord("today"), inputWord("tomorrow"), inputWord("next week"), strings.ToLower(localizedWeekday(time.Monday)))
}

// parseWeekday takes the English day names and their three-letter forms, plus
// the active language's names and abbreviations — the same tables the calendar
// draws its headers from.
func parseWeekday(s string) (time.Weekday, bool) {
	s = canonicalInputWord(s)
	days := map[string]time.Weekday{
		"monday":    time.Monday,
		"tuesday":   time.Tuesday,
		"wednesday": time.Wednesday,
		"thursday":  time.Thursday,
		"friday":    time.Friday,
		"saturday":  time.Saturday,
		"sunday":    time.Sunday,
		"mon":       time.Monday,
		"tue":       time.Tuesday,
		"wed":       time.Wednesday,
		"thu":       time.Thursday,
		"fri":       time.Friday,
		"sat":       time.Saturday,
		"sun":       time.Sunday,
	}
	if wd, ok := days[s]; ok {
		return wd, true
	}
	return 0, false
}

func nextWeekday(today time.Time, target time.Weekday) time.Time {
	current := today.Weekday()
	daysAhead := int(target) - int(current)
	if daysAhead <= 0 {
		daysAhead += 7
	}
	return today.AddDate(0, 0, daysAhead)
}

func parsePositiveInt(s string) (int, bool) {
	if len(s) == 0 {
		return 0, false
	}
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, false
		}
		n = n*10 + int(ch-'0')
	}
	return n, true
}

// dependentsOf returns the pending tasks that depend on id — the inbound
// side of the dependency graph ("Blocks"). Outbound dependencies live on the
// task itself; the inbound list only exists by scanning the full set.
func dependentsOf(todos []*todo.Todo, id string) []*todo.Todo {
	var out []*todo.Todo
	for i := range todos {
		if todos[i].Status != todo.Pending {
			continue
		}
		for _, dep := range todos[i].Dependencies {
			if dep == id {
				out = append(out, todos[i])
				break
			}
		}
	}
	return out
}
