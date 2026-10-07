package main

import (
	"crypto/sha256"
	"strings"
	"testing"
	"time"

	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"

	"github.com/Morteningemann86/xtratjek/todo"
	"github.com/charmbracelet/x/ansi"
)

func TestIsHomebrewCellarPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"Apple Silicon Homebrew", "/opt/homebrew/Cellar/tjek/1.30.0/bin/tjek", true},
		{"Intel Homebrew", "/usr/local/Cellar/tjek/1.30.0/bin/tjek", true},
		{"Linuxbrew", "/home/linuxbrew/.linuxbrew/Cellar/tjek/1.30.0/bin/tjek", true},
		{"unresolved Homebrew symlink", "/opt/homebrew/bin/tjek", false},
		{"manual install", "/usr/local/bin/tjek", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isHomebrewCellarPath(tt.path); got != tt.want {
				t.Errorf("isHomebrewCellarPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestSelfUpdateAsset(t *testing.T) {
	// The two original names are frozen: a binary already in the field looks for
	// the name it was built with, so renaming one breaks self-update for every
	// existing install.
	tests := []struct {
		goos      string
		goarch    string
		wantAsset string
		wantErr   bool
	}{
		{"linux", "amd64", "tjek", false},
		{"linux", "arm64", "tjek-linux-arm64", false},
		{"linux", "386", "", true},
		{"windows", "amd64", "tjek.exe", false},
		{"windows", "arm64", "", true},
		{"darwin", "arm64", "", true},
		{"freebsd", "amd64", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.goos+"/"+tt.goarch, func(t *testing.T) {
			asset, err := selfUpdateAsset(tt.goos, tt.goarch)
			if asset != tt.wantAsset {
				t.Errorf("selfUpdateAsset(%q) asset = %q, want %q", tt.goos, asset, tt.wantAsset)
			}
			if (err != nil) != tt.wantErr {
				t.Errorf("selfUpdateAsset(%q) error = %v, wantErr %v", tt.goos, err, tt.wantErr)
			}
		})
	}
}

// ── clamp ─────────────────────────────────────────────────────────────────────

func TestClamp(t *testing.T) {
	tests := []struct {
		name          string
		val, min, max int
		want          int
	}{
		{"in range", 5, 0, 10, 5},
		{"at min", 0, 0, 10, 0},
		{"at max", 10, 0, 10, 10},
		{"below min", -5, 0, 10, 0},
		{"above max", 99, 0, 10, 10},
		{"all equal", 5, 5, 5, 5},
		{"negative range", -3, -10, -1, -3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clamp(tt.val, tt.min, tt.max)
			if got != tt.want {
				t.Errorf("clamp(%d, %d, %d) = %d, want %d",
					tt.val, tt.min, tt.max, got, tt.want)
			}
		})
	}
}

// ── truncate ──────────────────────────────────────────────────────────────────

func TestTruncate(t *testing.T) {
	tests := []struct {
		name  string
		input string
		max   int
		want  string
	}{
		{"short unchanged", "hi", 10, "hi"},
		{"exact length", "hello", 5, "hello"},
		{"needs truncation", "hello world", 8, "hello w…"},
		{"max 3", "hello", 3, "he…"},
		{"max 4", "hello", 4, "hel…"},
		{"empty string", "", 5, ""},
		{"max 1 is the marker alone", "hello", 1, "…"},
		{"max 0", "hello", 0, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.input, tt.max)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q",
					tt.input, tt.max, got, tt.want)
			}
		})
	}
}

// ── padRight ──────────────────────────────────────────────────────────────────

func TestPadRight(t *testing.T) {
	tests := []struct {
		name  string
		input string
		width int
		want  string
	}{
		{"needs padding", "hi", 5, "hi   "},
		{"exact width", "hello", 5, "hello"},
		{"longer than width", "hello world", 5, "hello"},
		{"empty string", "", 3, "   "},
		{"width zero", "hi", 0, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := padRight(tt.input, tt.width)
			if got != tt.want {
				t.Errorf("padRight(%q, %d) = %q, want %q",
					tt.input, tt.width, got, tt.want)
			}
		})
	}
}

// ── wrapText ──────────────────────────────────────────────────────────────────

func TestWrapText(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		width     int
		wantLines int
	}{
		{"short text", "hello", 80, 1},
		{"exact width", "hello", 5, 1},
		{"wraps once", "hello world", 6, 2},
		{"long no spaces", "abcdefghij", 4, 3},
		{"empty string", "", 10, 0},
		{"width 0 uses 1", "hi", 0, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapText(tt.input, tt.width)
			if len(got) != tt.wantLines {
				t.Errorf("wrapText(%q, %d) = %d lines %v, want %d lines",
					tt.input, tt.width, len(got), got, tt.wantLines)
			}
		})
	}
}

// ── commentLineCount ──────────────────────────────────────────────────────────

func TestCommentLineCount(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		available int
		want      int
	}{
		{"empty", "", 80, 1},
		{"short text", "hello", 80, 1},
		{"exact fit", "hello", 5, 1},
		{"wraps to 2", "hello world", 6, 2},
		{"wraps to 3", "this is a longer sentence here", 10, 3},
		{"single char available", "hello", 1, 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := commentLineCount(tt.text, tt.available)
			if got != tt.want {
				t.Errorf("commentLineCount(%q, %d) = %d, want %d",
					tt.text, tt.available, got, tt.want)
			}
		})
	}
}

// ── formatDuration ────────────────────────────────────────────────────────────

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name  string
		input time.Duration
		want  string
	}{
		{"seconds", 30 * time.Second, "30s"},
		{"minutes", 5 * time.Minute, "5m"},
		{"hours and minutes", 2*time.Hour + 30*time.Minute, "2h 30m"},
		{"over a day", 25 * time.Hour, "1d 1h"},
		{"multiple days", 50 * time.Hour, "2d 2h"},
		{"exactly one minute", 60 * time.Second, "1m"},
		{"exactly one hour", 60 * time.Minute, "1h 0m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatDuration(tt.input)
			if got != tt.want {
				t.Errorf("formatDuration(%v) = %q, want %q",
					tt.input, got, tt.want)
			}
		})
	}
}

// ── formatDueShort ────────────────────────────────────────────────────────────

func TestFormatDueShort(t *testing.T) {
	applyLang(string(langEN))
	now := time.Date(2026, 7, 3, 8, 30, 0, 0, time.Local)
	cases := []struct {
		days int
		want string
	}{
		{0, "today"},
		{1, "1d"},
		{6, "6d"},
		{28, "28d"},
		{-2, "-2d"},
	}
	for _, c := range cases {
		due := now.AddDate(0, 0, c.days)
		if got := formatDueShort(due, now); got != c.want {
			t.Errorf("%+d days: got %q, want %q", c.days, got, c.want)
		}
	}
	// Far dates in the same year show only dd-mm (no year) to save space.
	farSameYear := now.AddDate(0, 0, 40) // still 2026
	if got := formatDueShort(farSameYear, now); got != farSameYear.Format("02-01") {
		t.Errorf("far date same year: got %q, want %q", got, farSameYear.Format("02-01"))
	}
	// Far dates in a different year include the 2-digit year.
	farOtherYear := time.Date(2027, 3, 15, 0, 0, 0, 0, time.Local)
	if got := formatDueShort(farOtherYear, now); got != farOtherYear.Format("02-01-06") {
		t.Errorf("far date other year: got %q, want %q", got, farOtherYear.Format("02-01-06"))
	}
	// Same applies to far overdue dates in a past year.
	pastOtherYear := time.Date(2025, 1, 10, 0, 0, 0, 0, time.Local)
	if got := formatDueShort(pastOtherYear, now); got != pastOtherYear.Format("02-01-06") {
		t.Errorf("far past date other year: got %q, want %q", got, pastOtherYear.Format("02-01-06"))
	}
}

// ── parseQuickAdd ─────────────────────────────────────────────────────────────

func TestParseQuickAdd(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantTitle string
		wantTags  []string
		wantProj  string
		wantPrio  todo.Priority
		wantSize  todo.Size
		wantDue   bool
	}{
		{
			name:      "plain title",
			input:     "Buy groceries",
			wantTitle: "Buy groceries",
			wantPrio:  todo.PriorityMedium,
		},
		{
			name:      "with tags",
			input:     "Fix bug #backend #urgent",
			wantTitle: "Fix bug",
			wantTags:  []string{"backend", "urgent"},
			wantPrio:  todo.PriorityMedium,
		},
		{
			name:      "tag token is canonicalized",
			input:     "Plan work #Deep-Work",
			wantTitle: "Plan work",
			wantTags:  []string{"deep-work"},
			wantPrio:  todo.PriorityMedium,
		},
		{
			name:      "priority high",
			input:     "Deploy release p:high",
			wantTitle: "Deploy release",
			wantPrio:  todo.PriorityHigh,
		},
		{
			name:      "priority medium short",
			input:     "Review PR p:m",
			wantTitle: "Review PR",
			wantPrio:  todo.PriorityMedium,
		},
		{
			name:      "priority low explicit",
			input:     "Chill task p:low",
			wantTitle: "Chill task",
			wantPrio:  todo.PriorityLow,
		},
		{
			name:      "with project",
			input:     "Write docs @tjek",
			wantTitle: "Write docs",
			wantProj:  "tjek",
			wantPrio:  todo.PriorityMedium,
		},
		{
			name:      "with due date",
			input:     "Submit report due:tomorrow",
			wantTitle: "Submit report",
			wantDue:   true,
			wantPrio:  todo.PriorityMedium,
		},
		{
			name:      "all together",
			input:     "Refactor cache #performance @tjek p:high due:+3d",
			wantTitle: "Refactor cache",
			wantTags:  []string{"performance"},
			wantProj:  "tjek",
			wantPrio:  todo.PriorityHigh,
			wantDue:   true,
		},
		{
			name:      "invalid due becomes title",
			input:     "Check due:gibberish",
			wantTitle: "Check due:gibberish",
			wantPrio:  todo.PriorityMedium,
		},
		{
			name:      "invalid priority becomes title",
			input:     "Task p:extreme",
			wantTitle: "Task p:extreme",
			wantPrio:  todo.PriorityMedium,
		},
		{
			name:      "empty tag ignored",
			input:     "Test # thing",
			wantTitle: "Test thing",
			wantPrio:  todo.PriorityMedium,
		},
		{
			name:      "empty input",
			input:     "",
			wantTitle: "",
			wantPrio:  todo.PriorityMedium,
		},
		{
			name:      "size small",
			input:     "Sweep porch size:s",
			wantTitle: "Sweep porch",
			wantPrio:  todo.PriorityMedium,
			wantSize:  todo.SizeSmall,
		},
		{
			name:      "size shortcut s:l",
			input:     "Rewrite scheduler s:l",
			wantTitle: "Rewrite scheduler",
			wantPrio:  todo.PriorityMedium,
			wantSize:  todo.SizeLarge,
		},
		{
			name:      "size large word",
			input:     "Rewrite scheduler size:large",
			wantTitle: "Rewrite scheduler",
			wantPrio:  todo.PriorityMedium,
			wantSize:  todo.SizeLarge,
		},
		{
			name:      "invalid size becomes title",
			input:     "Task size:huge",
			wantTitle: "Task size:huge",
			wantPrio:  todo.PriorityMedium,
			wantSize:  todo.SizeMedium,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseQuickAdd(tt.input)

			if got.title != tt.wantTitle {
				t.Errorf("title = %q, want %q", got.title, tt.wantTitle)
			}
			if got.priority != tt.wantPrio {
				t.Errorf("priority = %v, want %v", got.priority, tt.wantPrio)
			}
			if got.size != tt.wantSize {
				t.Errorf("size = %v, want %v", got.size, tt.wantSize)
			}
			if tt.wantProj != "" && got.project != tt.wantProj {
				t.Errorf("project = %q, want %q", got.project, tt.wantProj)
			}
			if tt.wantDue && got.dueDate.IsZero() {
				t.Error("expected due date to be set, got zero")
			}
			if !tt.wantDue && !got.dueDate.IsZero() {
				t.Errorf("expected no due date, got %v", got.dueDate)
			}
			if len(tt.wantTags) > 0 {
				if len(got.tags) != len(tt.wantTags) {
					t.Fatalf("tags = %v, want %v", got.tags, tt.wantTags)
				}
				for i, tag := range tt.wantTags {
					if got.tags[i] != tag {
						t.Errorf("tag[%d] = %q, want %q", i, got.tags[i], tag)
					}
				}
			}
		})
	}
}

func TestParseQuickAddRecurrence(t *testing.T) {
	cases := []struct {
		input     string
		wantTitle string
		wantRecur string
	}{
		{"Water plants r:daily", "Water plants", "daily"},
		{"Standup r:weekly @team", "Standup", "weekly"},
		{"Rent recur:monthly", "Rent", "monthly"},
		{"Birthday card r:yearly", "Birthday card", "yearly"},
		{"Run r:weekdays", "Run", "weekdays"},
		{"Backup r:3d", "Backup", "every:3d"},
		{"Bogus r:every-other-tuesday", "Bogus r:every-other-tuesday", ""},
		{"Plain task", "Plain task", ""},
	}
	for _, c := range cases {
		got := parseQuickAdd(c.input)
		if got.title != c.wantTitle {
			t.Errorf("input %q: title = %q, want %q", c.input, got.title, c.wantTitle)
		}
		if got.recurrence != c.wantRecur {
			t.Errorf("input %q: recurrence = %q, want %q", c.input, got.recurrence, c.wantRecur)
		}
	}
}

func TestParseQuickAddDeps(t *testing.T) {
	cases := []struct {
		input     string
		wantTitle string
		wantDeps  []string
	}{
		{"Deploy dep:^", "Deploy", []string{"^"}},
		{"Deploy dep:fd8502d1", "Deploy", []string{"fd8502d1"}},
		{"Deploy dep:a1b2 dep:^ #ops", "Deploy", []string{"a1b2", "^"}},
		{"Keep dep: in title", "Keep dep: in title", nil},
		{"No deps here", "No deps here", nil},
	}
	for _, c := range cases {
		got := parseQuickAdd(c.input)
		if got.title != c.wantTitle {
			t.Errorf("input %q: title = %q, want %q", c.input, got.title, c.wantTitle)
		}
		if len(got.deps) != len(c.wantDeps) {
			t.Errorf("input %q: deps = %v, want %v", c.input, got.deps, c.wantDeps)
			continue
		}
		for i := range c.wantDeps {
			if got.deps[i] != c.wantDeps[i] {
				t.Errorf("input %q: deps[%d] = %q, want %q", c.input, i, got.deps[i], c.wantDeps[i])
			}
		}
	}
}

// ── nameColWidth ──────────────────────────────────────────────────────────────

func TestNameColWidth(t *testing.T) {
	tests := []struct {
		name      string
		termWidth int
	}{
		{"narrow terminal", 60},
		{"medium terminal", 120},
		{"wide terminal", 200},
		{"very narrow", 30},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nameColWidth(tt.termWidth)
			if got < nameColMinWidth {
				t.Errorf("nameColWidth(%d) = %d, below minimum %d",
					tt.termWidth, got, nameColMinWidth)
			}
			if got > nameColMaxWidth {
				t.Errorf("nameColWidth(%d) = %d, above maximum %d",
					tt.termWidth, got, nameColMaxWidth)
			}
			// Within the clamp range it is a straight percentage of the width.
			if want := tt.termWidth * nameColWidthPct / 100; want >= nameColMinWidth && want <= nameColMaxWidth && got != want {
				t.Errorf("nameColWidth(%d) = %d, want %d", tt.termWidth, got, want)
			}
		})
	}
}

// ── taskListCols title growth ───────────────────────────────────────────────

// shownColsW reconstructs the fixed-column budget from a laid-out listCols so
// the no-overflow checks don't depend on taskListCols internals.
func shownColsW(c listCols) int {
	w := 0
	if c.showSize {
		w += c.sizeW
	}
	if c.showDue {
		w += c.dueW
	}
	if c.showLast {
		w += c.lastW
	}
	if c.showProject {
		w += c.projectW
	}
	return w
}

func TestTaskListColsTitleGrowsOnWideTerminal(t *testing.T) {
	tests := []struct {
		name       string
		termWidth  int
		contentMax int
		tagsMax    int
	}{
		{"wide + long titles", 200, 120, 0},
		{"wide + short titles", 200, 18, 0},
		{"medium + long titles", 120, 90, 0},
		{"narrow + long titles", 60, 90, 0},
		{"wide + long titles + tags", 200, 120, 40},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := taskListCols(tt.termWidth, false, tt.contentMax, tt.tagsMax, true, 8, 8)

			// Never wider than the longest title needs (+gap), but at least the
			// header label — growth must not produce an empty padded column.
			floor := len([]rune(tr("Active tasks"))) + listColGap
			want := tt.contentMax + listColGap
			if want < floor {
				want = floor
			}
			if c.titleW > want {
				t.Errorf("titleW = %d, exceeds content need %d", c.titleW, want)
			}

			// No-wrap contract: title + fixed cells + a tags cell that can at
			// least carry its "+N" count must fit the inner width. The cell
			// itself is sized at render time from what the row has left over
			// (renderTaskTagsFitted), so the columns only have to guarantee it
			// somewhere to live — reserving the widest row's full tag width
			// here is what used to clip every title in the list.
			const fixed = 6
			inner := tt.termWidth - 8
			tagsReserve := 0
			if tt.tagsMax > 0 {
				tagsReserve = tagsOverflowMinW
			}
			if total := c.titleW + fixed + shownColsW(c) + tagsReserve; total > inner {
				t.Errorf("titleW=%d + fixed + cols + tags = %d overflows inner %d",
					c.titleW, total, inner)
			}
		})
	}

	// On a wide terminal a long title must grow past the flat nameColMaxWidth
	// cap, filling slack the old hard cap left empty.
	if c := taskListCols(200, false, 120, 0, true, 8, 0); c.titleW <= nameColMaxWidth {
		t.Errorf("wide terminal titleW = %d, want > flat cap %d (should absorb slack)",
			c.titleW, nameColMaxWidth)
	}
	// But a short title still hugs its content — no needless sprawl.
	if c := taskListCols(200, false, 18, 0, true, 8, 0); c.titleW != 18+listColGap {
		t.Errorf("short title titleW = %d, want %d (hug content)", c.titleW, 18+listColGap)
	}
	// Reserving tag room must shrink the grown title vs. the no-tags case, so
	// the tags column survives.
	// At a width where the title cannot reach its full content need, the tags
	// reserve is what it gives way to.
	noTags := taskListCols(160, false, 120, 0, true, 8, 0)
	withTags := taskListCols(160, false, 120, 40, true, 8, 0)
	if !(withTags.titleW < noTags.titleW) {
		t.Errorf("titleW with tags reserve = %d, want < no-reserve %d",
			withTags.titleW, noTags.titleW)
	}
	// ...but only up to the cap. One tag-heavy row must not hold back an
	// unbounded amount of title width, because the chips degrade to "+N" and
	// the held-back cells would go unused.
	inner := 160 - 8
	huge := taskListCols(160, false, 120, inner, true, 8, 0)
	if held := noTags.titleW - huge.titleW; held > inner*tagsReservePct/100 {
		t.Errorf("a tag-heavy row held back %d cells of title, want at most %d%%  of inner (%d)",
			held, tagsReservePct, inner*tagsReservePct/100)
	}
}

// The Tags column is allowed to cost other columns their place, but only the
// three cells it takes to say "+N". A row that has tags and no room for them
// should still report that they exist.
func TestNarrowListKeepsRoomForTheTagCount(t *testing.T) {
	c := taskListCols(60, false, 40, 30, true, 8, 8)
	const fixed = 6
	inner := 60 - 8
	if left := inner - fixed - c.titleW - shownColsW(c); left < tagsOverflowMinW {
		t.Errorf("narrow list left %d cells for the tags cell, want >= %d", left, tagsOverflowMinW)
	}
}

// ── Due-column collapse ───────────────────────────────────────────────────────

// TestDueColumnCollapseWhenNoDueDates verifies that when no visible task has a
// due date the Due column is omitted entirely (showDue == false) and the header
// carries no "Due" label either.
func TestDueColumnCollapseWhenNoDueDates(t *testing.T) {
	// hasDue=false: column must be absent regardless of terminal width.
	c := taskListCols(120, false, 20, 0, false, 0, 0)
	if c.showDue {
		t.Error("showDue should be false when hasDue=false")
	}

	// hasDue=true: column must appear on a reasonably wide terminal.
	c = taskListCols(120, false, 20, 0, true, 2, 0)
	if !c.showDue {
		t.Error("showDue should be true when hasDue=true and there is room")
	}
}

// TestDueColumnAppearsWhenAtLeastOneDue verifies the end-to-end path: a list
// with no due dates shows no "Due" column header; adding one due date causes
// it to appear. Uses renderListHeader directly so we don't have to parse a
// full View() that may include "Due date:" in the detail panel.
func TestDueColumnAppearsWhenAtLeastOneDue(t *testing.T) {
	var b strings.Builder

	// No due dates: header must not contain the "Due" column label.
	noDueCols := taskListCols(120, false, 20, 0, false, 0, 0)
	b.Reset()
	renderListHeader(&b, 120, false, noDueCols, "")
	hdrNoDue := b.String()
	// The header label is "Due" padded to the Due column; when absent it must not appear.
	if strings.Contains(hdrNoDue, tr("Due")) {
		t.Errorf("no due dates: list header should not show 'Due', got: %q", hdrNoDue)
	}

	// With a due date: header must now contain "Due".
	withDueCols := taskListCols(120, false, 20, 0, true, 2, 0)
	b.Reset()
	renderListHeader(&b, 120, false, withDueCols, "")
	hdrWithDue := b.String()
	if !strings.Contains(hdrWithDue, tr("Due")) {
		t.Errorf("with due dates: list header should show 'Due', got: %q", hdrWithDue)
	}
}

// TestDueColumnNoWrapContractWithCollapse verifies the no-wrap contract holds
// when the Due column is collapsed: every rendered line must fit within the
// terminal width.
func TestDueColumnNoWrapContractWithCollapse(t *testing.T) {
	for _, width := range []int{40, 60, 80, 120} {
		m := modelWithTasks(t, todo.New("alpha"), todo.New("beta"), todo.New("gamma"))
		m.termWidth = width
		m.termHeight = 30
		m.refreshCaches()
		out := m.View()
		for n, line := range strings.Split(out, "\n") {
			if w := ansi.StringWidth(line); w > width {
				t.Errorf("width=%d: line %d is %d cells wide (Due collapsed): %q",
					width, n, w, line)
			}
		}
	}
}

// ── Project-column sizing ─────────────────────────────────────────────────────

// TestProjectColumnCollapseWhenNoProjects verifies that when no visible task
// has a project the Project column is omitted entirely (showProject == false,
// projectW == 0) and the header carries no "Project" label.
func TestProjectColumnCollapseWhenNoProjects(t *testing.T) {
	// widestProject=0: column must be absent regardless of terminal width.
	c := taskListCols(120, false, 20, 0, false, 0, 0)
	if c.showProject {
		t.Error("showProject should be false when widestProject=0")
	}
	if c.projectW != 0 {
		t.Errorf("projectW should be 0 when no projects, got %d", c.projectW)
	}

	// widestProject>0: column must appear on a reasonably wide terminal.
	c = taskListCols(120, false, 20, 0, false, 0, 5)
	if !c.showProject {
		t.Error("showProject should be true when widestProject>0 and there is room")
	}
	if c.projectW == 0 {
		t.Error("projectW should be non-zero when widestProject>0")
	}
}

// TestProjectColumnAppearsWhenAtLeastOneProject verifies the header shows the
// "Project" label when at least one visible task has a project set, and that
// no "Project" label appears in the header when all tasks lack a project.
func TestProjectColumnAppearsWhenAtLeastOneProject(t *testing.T) {
	var b strings.Builder

	// No projects: header must not contain the "Project" column label.
	noProjCols := taskListCols(120, false, 20, 0, false, 0, 0)
	b.Reset()
	renderListHeader(&b, 120, false, noProjCols, "")
	hdrNoProj := b.String()
	if strings.Contains(hdrNoProj, tr("Project")) {
		t.Errorf("no projects: list header should not show 'Project', got: %q", hdrNoProj)
	}

	// With a project: header must now contain "Project".
	withProjCols := taskListCols(120, false, 20, 0, false, 0, 7)
	b.Reset()
	renderListHeader(&b, 120, false, withProjCols, "")
	hdrWithProj := b.String()
	if !strings.Contains(hdrWithProj, tr("Project")) {
		t.Errorf("with projects: list header should show 'Project', got: %q", hdrWithProj)
	}
}

// TestProjectColumnHugsWidestEntry verifies that a wide layout lets the column
// hug the widest project name plus its gap instead of retaining the compact cap.
func TestProjectColumnHugsWidestEntry(t *testing.T) {
	projHdrW := len([]rune(tr("Project")))

	cases := []struct {
		widest int
		want   int
		desc   string
	}{
		{1, projHdrW + listColGap, "name below the header floors at header + gap"},
		{projHdrW - 2, projHdrW + listColGap, "name shorter than header floors at header + gap"},
		{projHdrW + 2, projHdrW + 2 + listColGap, "name wider than header: hug + gap"},
		{projectColCompactW - listColGap, projectColCompactW, "name at compact boundary"},
		{projectColCompactW + 15, projectColCompactW + 15 + listColGap, "long name expands past compact width"},
	}

	for _, tc := range cases {
		c := taskListCols(200, false, 20, 0, false, 0, tc.widest)
		if !c.showProject {
			t.Errorf("%s: showProject should be true", tc.desc)
			continue
		}
		if c.projectW != tc.want {
			t.Errorf("%s: projectW=%d, want %d", tc.desc, c.projectW, tc.want)
		}
	}
}

// TestProjectColumnNoWrapContractWithCollapse verifies the no-wrap contract
// holds when the Project column is collapsed: every rendered line must fit
// within the terminal width.
func TestProjectColumnNoWrapContractWithCollapse(t *testing.T) {
	for _, width := range []int{40, 60, 80, 120} {
		t1 := todo.New("alpha")
		t2 := todo.New("beta")
		t3 := todo.New("gamma")
		// no projects set — column should collapse
		m := modelWithTasks(t, t1, t2, t3)
		m.termWidth = width
		m.termHeight = 30
		m.refreshCaches()
		out := m.View()
		for n, line := range strings.Split(out, "\n") {
			if w := ansi.StringWidth(line); w > width {
				t.Errorf("width=%d: line %d is %d cells wide (Project collapsed): %q",
					width, n, w, line)
			}
		}
	}
}

// TestProjectColumnNoWrapContractWithProject verifies the no-wrap contract
// holds when tasks have a project assigned: every rendered line must fit
// within the terminal width.
func TestProjectColumnNoWrapContractWithProject(t *testing.T) {
	for _, width := range []int{40, 60, 80, 120} {
		t1 := todo.New("alpha")
		t1.Project = "myproject"
		t2 := todo.New("beta")
		t2.Project = "myproject"
		t3 := todo.New("gamma")
		t3.Project = "longerproject"
		m := modelWithTasks(t, t1, t2, t3)
		m.termWidth = width
		m.termHeight = 30
		m.refreshCaches()
		out := m.View()
		for n, line := range strings.Split(out, "\n") {
			if w := ansi.StringWidth(line); w > width {
				t.Errorf("width=%d: line %d is %d cells wide (Project shown): %q",
					width, n, w, line)
			}
		}
	}
}

// ── Tags-column collapse ──────────────────────────────────────────────────────

// TestTagsColumnCollapseWhenNoTags verifies that when no visible task has any
// tags, showTags is false and the "Tags" header label is not emitted.
func TestTagsColumnCollapseWhenNoTags(t *testing.T) {
	// tagsMax=0: showTags must be false regardless of terminal width.
	c := taskListCols(120, false, 20, 0, false, 0, 0)
	if c.showTags {
		t.Error("showTags should be false when tagsMax=0")
	}

	// tagsMax>0: showTags must be true on a reasonably wide terminal.
	c = taskListCols(120, false, 20, 30, false, 0, 0)
	if !c.showTags {
		t.Error("showTags should be true when tagsMax>0")
	}
}

// TestTagsColumnAppearsWhenAtLeastOneTag verifies the header shows the "Tags"
// label when at least one visible task has tags, and that no "Tags" label
// appears in the header when all tasks lack tags.
func TestTagsColumnAppearsWhenAtLeastOneTag(t *testing.T) {
	var b strings.Builder

	// No tags: header must not contain the "Tags" column label.
	noTagsCols := taskListCols(120, false, 20, 0, false, 0, 0)
	b.Reset()
	renderListHeader(&b, 120, false, noTagsCols, "")
	hdrNoTags := b.String()
	if strings.Contains(hdrNoTags, tr("Tags")) {
		t.Errorf("no tags: list header should not show 'Tags', got: %q", hdrNoTags)
	}

	// With tags: header must now contain "Tags".
	withTagsCols := taskListCols(120, false, 20, 30, false, 0, 0)
	b.Reset()
	renderListHeader(&b, 120, false, withTagsCols, "")
	hdrWithTags := b.String()
	if !strings.Contains(hdrWithTags, tr("Tags")) {
		t.Errorf("with tags: list header should show 'Tags', got: %q", hdrWithTags)
	}
}

// TestTagsColumnNoWrapContractWithCollapse verifies the no-wrap contract holds
// when the Tags column is collapsed: every rendered line must fit within the
// terminal width.
func TestTagsColumnNoWrapContractWithCollapse(t *testing.T) {
	for _, width := range []int{40, 60, 80, 120} {
		// no tags set — Tags header label should collapse
		m := modelWithTasks(t, todo.New("alpha"), todo.New("beta"), todo.New("gamma"))
		m.termWidth = width
		m.termHeight = 30
		m.refreshCaches()
		out := m.View()
		for n, line := range strings.Split(out, "\n") {
			if w := ansi.StringWidth(line); w > width {
				t.Errorf("width=%d: line %d is %d cells wide (Tags collapsed): %q",
					width, n, w, line)
			}
		}
	}
}

// TestTagsColumnNoWrapContractWithTags verifies the no-wrap contract holds
// when tasks have tags: every rendered line must fit within the terminal width.
func TestTagsColumnNoWrapContractWithTags(t *testing.T) {
	for _, width := range []int{40, 60, 80, 120} {
		t1 := todo.New("alpha")
		t1.Tags = []string{"work", "urgent"}
		t2 := todo.New("beta")
		t2.Tags = []string{"personal"}
		t3 := todo.New("gamma")
		m := modelWithTasks(t, t1, t2, t3)
		m.termWidth = width
		m.termHeight = 30
		m.refreshCaches()
		out := m.View()
		for n, line := range strings.Split(out, "\n") {
			if w := ansi.StringWidth(line); w > width {
				t.Errorf("width=%d: line %d is %d cells wide (Tags shown): %q",
					width, n, w, line)
			}
		}
	}
}

// ── copyTodos ─────────────────────────────────────────────────────────────────

func TestCopyTodos(t *testing.T) {
	original := []todo.Todo{
		{
			ID:           "task-1",
			Title:        "Original",
			Tags:         []string{"work", "urgent"},
			Dependencies: []string{"dep-1", "dep-2"},
			Comments:     []todo.Comment{{ID: "c1", Text: "hello"}},
		},
	}

	cp := copyTodos(original)

	// Verify values copied correctly
	if cp[0].ID != "task-1" {
		t.Errorf("ID = %q, want %q", cp[0].ID, "task-1")
	}
	if cp[0].Title != "Original" {
		t.Errorf("Title = %q, want %q", cp[0].Title, "Original")
	}
	if len(cp[0].Tags) != 2 {
		t.Fatalf("Tags len = %d, want 2", len(cp[0].Tags))
	}

	// Modify original — copy should be unaffected
	original[0].Title = "Modified"
	original[0].Tags[0] = "changed"
	original[0].Dependencies[0] = "changed-dep"
	original[0].Comments[0].Text = "changed-comment"

	if cp[0].Title != "Original" {
		t.Error("copy title was affected by original mutation")
	}
	if cp[0].Tags[0] != "work" {
		t.Error("copy tags were affected by original mutation")
	}
	if cp[0].Dependencies[0] != "dep-1" {
		t.Error("copy dependencies were affected by original mutation")
	}
	if cp[0].Comments[0].Text != "hello" {
		t.Error("copy comments were affected by original mutation")
	}
}

func TestCopyTodosEmpty(t *testing.T) {
	cp := copyTodos([]todo.Todo{})
	if len(cp) != 0 {
		t.Errorf("expected empty copy, got %d", len(cp))
	}
}

func TestCopyTodosNilSlices(t *testing.T) {
	// Task with no slices — should not panic
	original := []todo.Todo{
		{ID: "bare", Title: "Bare task"},
	}
	cp := copyTodos(original)
	if cp[0].ID != "bare" {
		t.Errorf("ID = %q, want %q", cp[0].ID, "bare")
	}
}

// parseManualEntry backs both the TUI 'T' shortcut and `tjek log`: bare
// durations anchor the entry to END now (the "I just spent 45m" reading),
// clock ranges are literal on today.
func TestParseManualEntry(t *testing.T) {
	now := time.Date(2026, 7, 3, 14, 0, 0, 0, time.Local)

	start, stop, err := parseManualEntry("45m", now)
	if err != nil {
		t.Fatalf("duration form: %v", err)
	}
	if !stop.Equal(now) || !start.Equal(now.Add(-45*time.Minute)) {
		t.Errorf("45m → [%v, %v], want [now-45m, now]", start, stop)
	}

	start, stop, err = parseManualEntry("10:00-11:30", now)
	if err != nil {
		t.Fatalf("range form: %v", err)
	}
	wantStart := time.Date(2026, 7, 3, 10, 0, 0, 0, time.Local)
	if !start.Equal(wantStart) || stop.Sub(start) != 90*time.Minute {
		t.Errorf("range → [%v, %v], want literal 10:00 + 90m", start, stop)
	}

	if _, _, err := parseManualEntry("banana", now); err == nil {
		t.Error("expected error for junk input")
	}
}

// ── Release lookup over the API ───────────────────────────────────────────────

// releaseServer stands in for api.github.com. Returns the base URL to point
// releaseAPIBase at, restoring the real one when the test ends.
func releaseServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	prev := releaseAPIBase
	releaseAPIBase = srv.URL
	t.Cleanup(func() {
		releaseAPIBase = prev
		srv.Close()
	})
	return srv
}

func TestFetchLatestReleaseParsesTagAndAssets(t *testing.T) {
	var gotPath, gotAccept, gotAgent string
	releaseServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAccept, gotAgent = r.URL.Path, r.Header.Get("Accept"), r.Header.Get("User-Agent")
		fmt.Fprint(w, `{"tag_name":"v1.20.0","assets":[
			{"name":"tjek","browser_download_url":"https://example.invalid/tjek"},
			{"name":"tjek.exe","browser_download_url":"https://example.invalid/tjek.exe"}]}`)
	})

	info, err := fetchLatestRelease()
	if err != nil {
		t.Fatalf("fetchLatestRelease: %v", err)
	}
	if info.TagName != "v1.20.0" {
		t.Errorf("tag = %q, want v1.20.0", info.TagName)
	}
	if gotPath != "/repos/"+releaseRepo+"/releases/latest" {
		t.Errorf("requested %q", gotPath)
	}
	// The API rejects requests with no User-Agent, and the Accept header is the
	// documented one — both are easy to drop and only fail against the real host.
	if gotAccept != "application/vnd.github+json" {
		t.Errorf("Accept = %q", gotAccept)
	}
	if !strings.HasPrefix(gotAgent, "tjek/") {
		t.Errorf("User-Agent = %q, want a tjek/… agent", gotAgent)
	}

	asset, err := findReleaseAsset(info, "tjek.exe")
	if err != nil || asset.URL != "https://example.invalid/tjek.exe" {
		t.Errorf("findReleaseAsset = %+v, %v", asset, err)
	}
	if _, err := findReleaseAsset(info, "tjek.dmg"); err == nil {
		t.Error("a missing asset should be an error, not an empty download")
	}
	if _, err := findReleaseAsset(releaseInfo{TagName: "v1", Assets: []releaseAsset{{Name: "tjek"}}}, "tjek"); err == nil {
		t.Error("an asset with no URL should be an error")
	}
}

// The failure an unauthenticated caller actually hits is the hourly rate limit,
// so it has to read as something other than a generic 403.
func TestFetchLatestReleaseSurfacesRateLimit(t *testing.T) {
	releaseServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
	})
	_, err := fetchLatestRelease()
	if err == nil || !strings.Contains(err.Error(), "rate limit") {
		t.Errorf("error = %v, want it to mention the rate limit", err)
	}
}

func TestFetchLatestReleaseErrors(t *testing.T) {
	releaseServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	if _, err := fetchLatestRelease(); err == nil || !strings.Contains(err.Error(), "no releases") {
		t.Errorf("404 error = %v", err)
	}

	releaseServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tag_name":""}`)
	})
	if _, err := fetchLatestRelease(); err == nil {
		t.Error("a release with no tag name should be an error")
	}

	releaseServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `not json`)
	})
	if _, err := fetchLatestRelease(); err == nil {
		t.Error("unparseable JSON should be an error")
	}
}

func TestDownloadReleaseAsset(t *testing.T) {
	const payload = "#!/bin/sh\necho tjek\n"
	// releaseServer, not a bare httptest server: asset downloads are pinned to
	// GitHub hosts (checkAssetURL), and the stand-in is trusted only because it
	// is the same origin releaseAPIBase points at — which is how the real flow
	// works too.
	srv := releaseServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/empty" {
			return
		}
		if r.URL.Path == "/missing" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fmt.Fprint(w, payload)
	})

	dst := filepath.Join(t.TempDir(), "tjek")
	digest, err := downloadReleaseAsset(releaseAsset{Name: "tjek", URL: srv.URL + "/tjek"}, dst)
	if err != nil {
		t.Fatalf("downloadReleaseAsset: %v", err)
	}
	// The digest is what the integrity check compares, so it has to be of the
	// bytes that landed on disk.
	if want := fmt.Sprintf("%x", sha256.Sum256([]byte(payload))); digest != want {
		t.Errorf("digest = %s, want %s", digest, want)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != payload {
		t.Fatalf("downloaded %q (%v), want the payload", got, err)
	}
	// The staged file is executed after being renamed into place, so it has to
	// land executable.
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		t.Errorf("mode = %v, want the executable bits set", info.Mode().Perm())
	}

	if _, err := downloadReleaseAsset(releaseAsset{URL: srv.URL + "/missing"}, dst); err == nil {
		t.Error("a 404 should not be reported as a successful download")
	}
	if _, err := downloadReleaseAsset(releaseAsset{URL: srv.URL + "/empty"}, dst); err == nil {
		t.Error("an empty body should not be installed over the binary")
	}
}

// ── Editor resolution ────────────────────────────────────────────────────────

// resolveEditorCmd memoizes, because a miss walks the whole PATH for six
// candidates — thousands of stat syscalls on Windows, where PATHEXT multiplies
// every probe. The cache is keyed on the inputs, not a sync.Once, so a changed
// $EDITOR still takes effect; this pins that.
func TestResolveEditorCmdHonoursAChangedEditor(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Skipf("no executable path on this platform: %v", err)
	}

	t.Setenv("EDITOR", self)
	if got := resolveEditorCmd(); got != self {
		t.Fatalf("resolveEditorCmd() = %q, want the $EDITOR we set (%q)", got, self)
	}
	if got := resolveEditorCmd(); got != self {
		t.Fatalf("second call = %q, want the same answer %q", got, self)
	}

	t.Setenv("EDITOR", "tjek-definitely-not-an-editor-zzz")
	if got := resolveEditorCmd(); got == self {
		t.Error("a stale cache answered with the previous $EDITOR after it changed")
	}
}

// ── Update integrity ─────────────────────────────────────────────────────────

func TestChecksumForParsesSha256sumOutput(t *testing.T) {
	sums := []byte(
		"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855  tjek\n" +
			"a1b2c3d4e5f60718293a4b5c6d7e8f90112233445566778899aabbccddeeff00 *tjek.exe\n" +
			"\n" +
			"not a checksum line\n")

	got, err := checksumFor(sums, "tjek")
	if err != nil || got != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Errorf("tjek → %q, %v", got, err)
	}
	// GNU coreutils marks binary mode with a leading '*' on the name.
	if got, err := checksumFor(sums, "tjek.exe"); err != nil || got == "" {
		t.Errorf("binary-mode entry not found: %q, %v", got, err)
	}
	if _, err := checksumFor(sums, "tjek-linux-arm64"); err == nil {
		t.Error("an asset with no entry must be an error, not an empty checksum")
	}
	// A truncated or non-hex digest is corruption, not a match to compare.
	for _, bad := range []string{"abc123  tjek\n", "zzzz0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855  tjek\n"} {
		if _, err := checksumFor([]byte(bad), "tjek"); err == nil {
			t.Errorf("malformed line %q was accepted", bad)
		}
	}
}

// The update path must refuse anything it cannot check: a release with no
// SHA256SUMS, an asset missing from it, or bytes that hash to something else.
func TestDownloadVerifiedAssetRefusesUnverifiedBinaries(t *testing.T) {
	const payload = "#!/bin/sh\necho tjek\n"
	good := fmt.Sprintf("%x", sha256.Sum256([]byte(payload)))

	var sumsBody string
	srv := releaseServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tjek":
			fmt.Fprint(w, payload)
		case "/sums":
			fmt.Fprint(w, sumsBody)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	release := func(withSums bool) releaseInfo {
		info := releaseInfo{TagName: "v9.9.9", Assets: []releaseAsset{
			{Name: "tjek", URL: srv.URL + "/tjek"},
		}}
		if withSums {
			info.Assets = append(info.Assets, releaseAsset{Name: sha256SumsAsset, URL: srv.URL + "/sums"})
		}
		return info
	}

	t.Run("matching checksum installs", func(t *testing.T) {
		sumsBody = good + "  tjek\n"
		dst := filepath.Join(t.TempDir(), "tjek")
		if err := downloadVerifiedAsset(release(true), "tjek", dst); err != nil {
			t.Fatalf("a matching checksum should verify: %v", err)
		}
		if b, err := os.ReadFile(dst); err != nil || string(b) != payload {
			t.Errorf("staged file = %q (%v), want the payload", b, err)
		}
	})

	t.Run("mismatched checksum refuses and removes the file", func(t *testing.T) {
		sumsBody = strings.Repeat("a", 64) + "  tjek\n"
		dst := filepath.Join(t.TempDir(), "tjek")
		err := downloadVerifiedAsset(release(true), "tjek", dst)
		if err == nil {
			t.Fatal("a mismatched checksum was installed")
		}
		if !strings.Contains(err.Error(), "checksum mismatch") {
			t.Errorf("error = %v, want it to name the mismatch", err)
		}
		if _, statErr := os.Stat(dst); statErr == nil {
			t.Error("the rejected binary was left on disk")
		}
	})

	t.Run("a release without SHA256SUMS is refused", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "tjek")
		err := downloadVerifiedAsset(release(false), "tjek", dst)
		if err == nil {
			t.Fatal("an unverifiable release was installed")
		}
		if !strings.Contains(err.Error(), sha256SumsAsset) {
			t.Errorf("error = %v, want it to say what is missing", err)
		}
	})

	t.Run("SHA256SUMS without this asset is refused", func(t *testing.T) {
		sumsBody = good + "  some-other-binary\n"
		dst := filepath.Join(t.TempDir(), "tjek")
		if err := downloadVerifiedAsset(release(true), "tjek", dst); err == nil {
			t.Fatal("an asset with no published checksum was installed")
		}
	})
}

// ── Row label / column metric agreement ──────────────────────────────────────

// The title column is sized from taskRowLabel and drawn from taskRowLabel, so
// the reserved width and the drawn width cannot drift. This used to be two
// hand-kept copies of the same arithmetic, one in cache.go and one in the
// renderer, and every badge added since had to be added to both.
func TestTitleColumnReservesExactlyWhatTheRowDraws(t *testing.T) {
	task := todo.New("Ship the release")
	task.Recurrence = "weekly"
	sub := todo.NewSubtask("write notes", task.ID)
	m := modelWithTasks(t, task, sub)
	m.termWidth = 200 // wide enough that nothing is clipped
	m.refreshCaches()

	cur := m.get(task.ID)
	if cur == nil {
		t.Fatal("task should be in the store")
	}
	prefix, text, badges := m.taskRowLabel(cur)
	want := taskRowLabelWidth(prefix, text, badges)
	if m.cache.activeColContentMax != want {
		t.Errorf("reserved content width = %d, row draws %d (%q|%q|%q)",
			m.cache.activeColContentMax, want, prefix, text, badges)
	}
	// And the badges really are on the label, not folded into the title.
	if text != task.Title {
		t.Errorf("title text = %q, want the bare title %q", text, task.Title)
	}
	for _, badge := range []string{"↻", "(0/1)"} {
		if !strings.Contains(badges, badge) {
			t.Errorf("badges %q should carry %q", badges, badge)
		}
	}
}

// ── Tag cell ─────────────────────────────────────────────────────────────────

func TestTagCellClipsThenCounts(t *testing.T) {
	tags := []string{"bug", "ci", "release"}
	full := 1 + rowTagsWidth(tags)

	check := func(avail int, want string) {
		t.Helper()
		got, w := renderTaskTagsClipped(tags, avail, false)
		if plain := ansi.Strip(got); plain != want {
			t.Errorf("avail %d: cell = %q, want %q", avail, plain, want)
		}
		if w > avail {
			t.Errorf("avail %d: cell drew %d cells", avail, w)
		}
		if aw := ansi.StringWidth(got); aw != w {
			t.Errorf("avail %d: reported width %d disagrees with the rendered %d", avail, w, aw)
		}
	}
	// Everything fits: every tag, no count.
	check(full+10, " #bug #ci #release")
	// One cell short: the last tag is clipped, not dropped.
	check(full-1, " #bug #ci #relea…")
	// Too little room to clip it to a couple of letters: it is counted.
	check(1+len("#bug #ci +1"), " #bug #ci +1")
	// Room for the first tag and a count, but not a clip of the second.
	check(1+len("#bug +2"), " #bug +2")
	// Room for nothing but the count.
	check(tagsOverflowMinW, " +3")
	// No room at all: draw nothing rather than overflow the row.
	check(1, "")
}

// A list whose longest title is shorter than its own column header sized the
// title column to exactly the header, and the header then ran into the next
// one: "Active tasksScore".
func TestShortTitlesStillLeaveAGapAfterTheHeader(t *testing.T) {
	c := taskListCols(100, false, 4, 0, false, 0, 0)
	if c.titleW < len([]rune(tr("Active tasks")))+listColGap {
		t.Errorf("titleW = %d, want at least the header plus its gap (%d)",
			c.titleW, len([]rune(tr("Active tasks")))+listColGap)
	}
}

// The list prints an overdue date as "-2d", so "-2d" types it back in.
func TestParseDueDateCountsBack(t *testing.T) {
	now := time.Date(2026, 9, 29, 10, 0, 0, 0, time.Local)
	for in, want := range map[string]string{
		"-2d": "27-09-26", "+2d": "01-10-26", "-1w": "22-09-26", "-1m": "29-08-26",
	} {
		got, err := parseDueDateAt(in, now)
		if err != nil || got.Format("02-01-06") != want {
			t.Errorf("parseDueDateAt(%q) = %v, %v; want %s", in, got.Format("02-01-06"), err, want)
		}
	}
	if _, err := parseDueDateAt("-0d", now); err == nil {
		t.Error("-0d should be refused, as +0d is")
	}
	if p := parseQuickAdd("Fix the tap due:-2d"); p.title != "Fix the tap" || p.dueDate.IsZero() {
		t.Errorf("quick-add should take due:-2d as a date, got title %q", p.title)
	}
}

// A column heading much wider than its values strands cells on every row. In
// Danish "Størrelse" over a single s/m/l pushed Project off the list at a
// width where English still shows it.
func TestTranslatedListHeadingsKeepTheProjectColumn(t *testing.T) {
	defer applyLang(string(langEN))
	for _, lang := range availableLanguages {
		applyLang(string(lang))
		c := taskListCols(75, false, 30, 12, true, 5, 8)
		if !c.showProject {
			t.Errorf("%s: the Project column dropped at a width English keeps it (%+v)", lang, c)
		}
	}
}
