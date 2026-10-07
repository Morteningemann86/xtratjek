package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
	"github.com/charmbracelet/x/ansi"
)

// testBoard is a board config with the given column list.
func testBoard(stages []string) boardConfig {
	c := defaultBoardConfig()
	c.setStages(stages)
	return c
}

func TestStagesFromSettings(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty falls back to defaults", nil, defaultStages()},
		{"all-blank falls back to defaults", []string{"", "  "}, defaultStages()},
		{"trims and keeps order", []string{" Todo ", "Doing"}, []string{"Todo", "Doing"}},
		{"dedupes case-insensitively onto first spelling", []string{"Todo", "todo", "Done-ish"}, []string{"Todo", "Done-ish"}},
		// One name cannot be both the work and the finish line, so the missing
		// half is supplied rather than leaving a board with no working column.
		{"a lone column gains a Done column", []string{"Doing"}, []string{"Doing", "Done"}},
		{"a lone Done column gains a working one", []string{"done"}, []string{"Backlog", "done"}},
	}
	for _, c := range cases {
		if got := stagesFromSettings(appSettings{Stages: c.in}); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: stagesFromSettings(%v) = %v, want %v", c.name, c.in, got, c.want)
		}
	}
}

func TestStageIndexAndCanonical(t *testing.T) {
	c := testBoard([]string{"Todo", "Doing", "Waiting", "Shipped"})
	{
		for stage, want := range map[string]int{
			"":        0, // fresh task → first column
			"Todo":    0,
			"doing":   1, // case-insensitive
			"Waiting": 2,
			"Review":  0, // renamed-away stage strands visibly in the first column
			// The last column is Done, not a stage: a task carrying its name is
			// still pending, so it belongs in the first column and not among the
			// completed work.
			"Shipped": 0,
		} {
			if got := c.stageIndex(stage); got != want {
				t.Errorf("stageIndex(%q) = %d, want %d", stage, got, want)
			}
		}

		if name, ok := c.canonicalStage(" doing "); !ok || name != "Doing" {
			t.Errorf("canonicalStage(' doing ') = %q/%v, want Doing/true", name, ok)
		}
		if _, ok := c.canonicalStage("nope"); ok {
			t.Error("canonicalStage('nope') should not resolve")
		}
		// --stage <the Done column> would be a second way to complete a task,
		// which is exactly what the one-close-path rule forbids.
		if _, ok := c.canonicalStage("Shipped"); ok {
			t.Error("canonicalStage resolved the Done column — --stage would complete a task")
		}
	}
}

// The last configured column *is* Done: renaming it renames the heading the
// completed cards sit under, and does not add a fifth column or turn the
// renamed name into a stage a pending task can hold.
func TestLastColumnIsDoneWhateverItIsCalled(t *testing.T) {
	pending := todo.New("still going")
	pending.Stage = "Review"
	finished := todo.New("shipped")
	finished.Status = todo.Done
	finished.CompletedAt = time.Now()

	m := newTagModel(pending, finished)
	m.boardCfg.setStages([]string{"Backlog", "In progress", "Review", "Shipped"})
	{
		m.markCacheDirty()
		m.ensureCache()
		cols := m.boardColumns()
		if len(cols) != 4 {
			t.Fatalf("columns = %d, want the 4 configured ones", len(cols))
		}
		if titles := m.boardColTitles(); titles[len(titles)-1] != "✓ Shipped" {
			t.Errorf("last column heading = %q, want the configured name behind its ✓", titles[len(titles)-1])
		}
		if got := cols[m.boardCfg.doneColumn()]; len(got) != 1 || got[0].Title != "Shipped" {
			t.Errorf("Done column = %v, want the completed task", got)
		}
		if got := cols[2]; len(got) != 1 || got[0].Title != "Still going" {
			t.Errorf("Review column = %v, want the pending task", got)
		}
		// And the arrows in the detail pane still cannot walk a task into it.
		seen := map[string]bool{}
		stage := ""
		for i := 0; i < len(m.boardCfg.stages)+2; i++ {
			stage = m.boardCfg.cycleStage(stage, 1)
			seen[stage] = true
		}
		if seen["Shipped"] {
			t.Error("cycleStage reached the Done column")
		}
		if len(seen) != len(m.boardCfg.pending()) {
			t.Errorf("cycleStage visited %d columns, want the %d working ones", len(seen), len(m.boardCfg.pending()))
		}
	}
}

func TestBoardColumnsSplitByStage(t *testing.T) {
	backlog := todo.New("fresh task") // empty stage → first column
	doing := todo.New("moving task")
	doing.Stage = "In progress"
	review := todo.New("almost there")
	review.Stage = "Review"
	sub := todo.New("subtask hidden from the board")
	sub.ParentID = backlog.ID
	finished := todo.New("shipped")
	finished.Status = todo.Done
	finished.CompletedAt = time.Now()

	m := newTagModel(backlog, doing, review, sub, finished)
	cols := m.boardColumns()

	if len(cols) != 4 {
		t.Fatalf("columns = %d, want 4 (3 working columns + Done)", len(cols))
	}
	for i, want := range []string{"Fresh task", "Moving task", "Almost there", "Shipped"} {
		if len(cols[i]) != 1 || cols[i][0].Title != want {
			t.Errorf("column %d = %v, want single card %q", i, cols[i], want)
		}
	}
}

func TestBoardRenderWideAndStacked(t *testing.T) {
	a := todo.New("alpha")
	b := todo.New("beta")
	b.Stage = "Review"
	m := newTagModel(a, b)
	m.tab = tabBoard
	m.termWidth = 120
	m.termHeight = 30

	out := ansi.Strip(m.renderBoardList())
	for _, want := range []string{"Backlog  1", "In progress  0", "Review  1", "Done  0", "│ Alpha"} {
		if !strings.Contains(out, want) {
			t.Errorf("wide board missing %q:\n%s", want, out)
		}
	}
	availW := m.termWidth - 8
	for _, line := range strings.Split(m.renderBoardList(), "\n") {
		if w := ansi.StringWidth(line); w > availW {
			t.Errorf("board line exceeds availW (%d > %d): %q", w, availW, ansi.Strip(line))
		}
	}

	// Narrow terminal degrades to the stacked, full-width form.
	m.termWidth = 50
	stacked := ansi.Strip(m.renderBoardList())
	if !strings.Contains(stacked, "Backlog  1") || !strings.Contains(stacked, "Review  1") {
		t.Errorf("stacked board missing headers:\n%s", stacked)
	}
}

func TestBoardSelectionClamps(t *testing.T) {
	a := todo.New("only card")
	m := newTagModel(a)
	m.board.col = 99
	m.board.cursor = 42
	cols := m.boardColumns()
	col, cursor := m.boardSelection(cols)
	if col != len(cols)-1 || cursor != 0 {
		t.Errorf("clamped selection = (%d,%d), want last column, cursor 0", col, cursor)
	}
	if sel := m.boardSelectedTask(); sel != nil {
		t.Errorf("selection on empty Done column should be nil, got %q", sel.Title)
	}
	m.board.col = 0
	if sel := m.boardSelectedTask(); sel == nil || sel.Title != "Only card" {
		t.Errorf("selected task = %v, want Only card", sel)
	}
}

func TestParseStagesInput(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"splits and trims", "Todo, Doing , Waiting", []string{"Todo", "Doing", "Waiting"}},
		{"drops blanks from stray commas", "Todo,,Doing,", []string{"Todo", "Doing"}},
		{"dedupes like a hand-edited settings.json", "Todo, todo", []string{"Todo", "Done"}},
		{"empty line falls back to the defaults", "   ", defaultStages()},
	}
	for _, c := range cases {
		if got, _, _, _ := parseStagesInput(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: parseStagesInput(%q) = %v, want %v", c.name, c.in, got, c.want)
		}
	}
}

// A stage list edited in Settings must carry its cards along: renaming a
// column in place keeps them where they were, and dropping the last column
// hands them to the new last one, instead of stageIndex silently dumping every
// stranded card into the first column.
func TestStageRemap(t *testing.T) {
	cases := []struct {
		name     string
		old, new []string
		want     map[string]string
	}{
		{
			"rename in place keeps the column",
			[]string{"Backlog", "In progress", "Review", "Done"},
			[]string{"Backlog", "In progress", "QA", "Done"},
			map[string]string{"review": "QA"},
		},
		{
			"dropped tail column hands cards to the last working one",
			[]string{"Backlog", "In progress", "Review", "Done"},
			[]string{"Backlog", "In progress", "Done"},
			map[string]string{"review": "In progress"},
		},
		{
			"unchanged names are not remapped",
			[]string{"Backlog", "Doing", "Done"},
			[]string{"Backlog", "Doing", "Review", "Done"},
			map[string]string{},
		},
		{
			"case-only rename is a no-op for the cards",
			[]string{"Backlog", "Done"},
			[]string{"backlog", "Done"},
			map[string]string{},
		},
		{
			// Done cards are found by status, so the last column's heading can
			// change without touching a single stored stage.
			"renaming the Done column moves nothing",
			[]string{"Backlog", "Doing", "Done"},
			[]string{"Backlog", "Doing", "Shipped"},
			map[string]string{},
		},
	}
	for _, c := range cases {
		if got := stageRemap(c.old, c.new); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: stageRemap(%v, %v) = %v, want %v", c.name, c.old, c.new, got, c.want)
		}
	}
}

// A column's icon is written in brackets before its name, the way the status
// box shows it. One cell or it is refused; the Done column keeps ✓.
func TestParseStagesInputReadsIcons(t *testing.T) {
	stages, icons, doneIcon, err := parseStagesInput("[B] Backlog, [>] In progress, Review, [✓] Shipped")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Backlog", "In progress", "Review", "Shipped"}; !reflect.DeepEqual(stages, want) {
		t.Errorf("stages = %v, want %v", stages, want)
	}
	if icons["backlog"] != "B" || icons["in progress"] != ">" || icons["review"] != "" {
		t.Errorf("icons = %v", icons)
	}
	if doneIcon != "" {
		t.Errorf("the ✓ the editor shows on the last column is not news, got %q", doneIcon)
	}
	if _, _, doneIcon, _ := parseStagesInput("Todo, [x] Done"); doneIcon != "x" {
		t.Errorf("another icon on the last column should be reported, got %q", doneIcon)
	}
	if _, _, _, err := parseStagesInput("[🚀] Launch, Done"); err == nil {
		t.Error("a two-cell emoji should be refused")
	}
	if _, _, _, err := parseStagesInput("[ab] Launch, Done"); err == nil {
		t.Error("two characters should be refused")
	}

	var c boardConfig
	c.setColumns(stages, icons)
	if got := c.stagesDisplay(); got != "[B] Backlog, [>] In progress, Review, Shipped" {
		t.Errorf("the editor pre-fill should read back as typed, the fixed ✓ left out: %q", got)
	}
	if _, _, _, err := parseStagesInput("[✓] Ready, Done"); !errors.Is(err, errDoneIconTaken) {
		t.Errorf("✓ belongs to the last column; on a working one it should be refused, got %v", err)
	}
}

// Once any column has an icon, a pending task's box shows its column's mark,
// blank for a column without one, ahead of overdue and started. Done is ✓;
// a subtask, a hidden board or a board without icons keep the usual marks.
func TestStatusBoxShowsTheColumnIcon(t *testing.T) {
	var c boardConfig
	c.shown = true
	c.setColumns([]string{"Backlog", "Doing", "Review", "Done"}, map[string]string{"doing": "D", "review": "R"})

	late := todo.New("late")
	late.DueDate = time.Now().AddDate(0, 0, -2)
	late.SetStage("Review")
	started := todo.New("started")
	started.TimeEntries = []todo.TimeEntry{{StartedAt: time.Now().Add(-time.Hour), StoppedAt: time.Now()}}
	started.SetStage("Doing")
	fresh := todo.New("fresh") // no stage: the first column, which has no icon
	done := todo.New("done")
	done.Status = todo.Done
	sub := todo.New("sub")
	sub.ParentID = late.ID
	sub.DueDate = late.DueDate

	for _, tc := range []struct {
		t    *todo.Todo
		want string
	}{{&late, "[R]"}, {&started, "[D]"}, {&fresh, "[ ]"}, {&done, "[✓]"}, {&sub, "[!]"}} {
		if got := c.statusBox(tc.t); got != tc.want {
			t.Errorf("%s: box = %q, want %q", tc.t.Title, got, tc.want)
		}
	}
	c.shown = false
	if got := c.statusBox(&late); got != "[!]" {
		t.Errorf("with the board hidden the box keeps its usual marks, got %q", got)
	}
	c.shown = true
	c.setColumns(c.stages, nil)
	if got := c.statusBox(&started); got != "[>]" {
		t.Errorf("without icons the box keeps its usual marks, got %q", got)
	}
	if got := c.columnIcon(c.doneColumn()); got != "✓" {
		t.Errorf("the Done column's mark is always ✓, got %q", got)
	}
}
