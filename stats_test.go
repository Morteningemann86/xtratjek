package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
	"github.com/charmbracelet/x/ansi"
)

func statsTodos() []todo.Todo {
	now := time.Now()
	var todos []todo.Todo
	for i := 0; i < 6; i++ {
		td := todo.New(fmt.Sprintf("active task %d with a longish title", i))
		td.CreatedAt = now.AddDate(0, 0, -i*2)
		if i == 0 {
			td.Priority = todo.PriorityHigh
			td.DueDate = now.AddDate(0, 0, -2)
		}
		todos = append(todos, td)
	}
	d := todo.New("done one")
	d.Status = todo.Done
	d.CompletedAt = now
	d.CreatedAt = now.AddDate(0, 0, -3)
	return append(todos, d)
}

// TestStatsTwoColumnLayout checks the stats page collapses into two columns on a
// wide terminal (shorter) and falls back to one column when narrow.
func TestStatsTwoColumnLayout(t *testing.T) {
	todos := statsTodos()

	wide := newTagModel(todos...)
	wide.termWidth = 120
	wide.tab = tabStats
	wide.refreshCaches()
	wideLines := strings.Split(strings.TrimRight(wide.renderStatsList(), "\n"), "\n")

	narrow := newTagModel(todos...)
	narrow.termWidth = 70
	narrow.tab = tabStats
	narrow.refreshCaches()
	narrowLines := strings.Split(strings.TrimRight(narrow.renderStatsList(), "\n"), "\n")

	if len(wideLines) >= len(narrowLines) {
		t.Errorf("two-column layout (%d lines) should be shorter than single column (%d)",
			len(wideLines), len(narrowLines))
	}

	pairFound := false
	for _, l := range wideLines {
		if strings.Contains(l, "Workload") && strings.Contains(l, "Throughput") {
			pairFound = true
		}
	}
	if !pairFound {
		t.Errorf("expected a side-by-side section row in two-column layout, got:\n%s",
			strings.Join(wideLines, "\n"))
	}

	// Both Flow windows are present (symmetric 3-per-side layout).
	joined := strings.Join(wideLines, "\n")
	for _, want := range []string{"Flow (last 7 days)", "Flow (last 30 days)", "vs prior 30d"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected %q in stats page, got:\n%s", want, joined)
		}
	}

	// Column content may not exceed the inner width, or it would wrap/clip and
	// lose data. (The divider is a deliberate full-bleed rule, excluded here.)
	for _, l := range wideLines {
		if strings.Contains(l, "─") {
			continue
		}
		if w := ansi.StringWidth(l); w > wide.termWidth-8 {
			t.Errorf("stats line exceeds inner width %d (%d cells): %q", wide.termWidth-8, w, l)
		}
	}
}

// TestStatsThreeColumns verifies the stats list uses three columns on a wide
// terminal, keeping the page short while showing both Flow blocks in full.
func TestStatsThreeColumns(t *testing.T) {
	todos := statsTodos()

	wide := newTagModel(todos...)
	wide.termWidth = 140
	wide.tab = tabStats
	wide.refreshCaches()
	wideLines := strings.Split(strings.TrimRight(wide.renderStatsList(), "\n"), "\n")

	two := newTagModel(todos...)
	two.termWidth = 100
	two.tab = tabStats
	two.refreshCaches()
	twoLines := strings.Split(strings.TrimRight(two.renderStatsList(), "\n"), "\n")

	if len(wideLines) >= len(twoLines) {
		t.Errorf("three columns (%d lines) should be shorter than two (%d)", len(wideLines), len(twoLines))
	}

	threeWide := false
	for _, l := range wideLines {
		if strings.Contains(l, "Workload") && strings.Contains(l, "Flow (last 7 days)") {
			threeWide = true
		}
	}
	if !threeWide {
		t.Errorf("expected a 3-column header row at width 140:\n%s", strings.Join(wideLines, "\n"))
	}

	joined := strings.Join(wideLines, "\n")
	if !strings.Contains(joined, "Flow (last 30 days)") || !strings.Contains(joined, "vs prior 30d") {
		t.Errorf("Flow (last 30 days) must remain fully present in 3-col:\n%s", joined)
	}
	for _, l := range wideLines {
		if strings.Contains(l, "─") {
			continue
		}
		if w := ansi.StringWidth(l); w > wide.termWidth-8 {
			t.Errorf("3-col line exceeds inner width %d (%d): %q", wide.termWidth-8, w, l)
		}
	}
}

// TestStatsHistogram checks the activity histogram for every range: the right
// title shows, nothing exceeds the pane's inner width (which would wrap), and
// the empty state is handled.
func TestStatsHistogram(t *testing.T) {
	now := time.Now()
	var todos []todo.Todo
	for i := 0; i < 40; i++ {
		td := todo.New(fmt.Sprintf("done %d", i))
		td.Status = todo.Done
		td.CompletedAt = now.AddDate(0, 0, -(i * 4)) // spread over ~160 days
		todos = append(todos, td)
	}

	ranges := map[statsRangeMode]string{
		statsRange7Days:   "Last 7 days",
		statsRange30Days:  "Last 30 days",
		statsRange6Months: "Last 26 weeks",
	}
	for _, w := range []int{40, 60, 100, 140} {
		for r, title := range ranges {
			m := newTagModel(todos...)
			m.termWidth = w
			m.termHeight = 44
			m.tab = tabStats
			m.statsRange = r
			m.refreshCaches()
			// The range, the count and the legend live on the panel's
			// border title now — the pane itself is nothing but chart.
			panelTitle := m.statsPanelTitle()
			if !strings.Contains(panelTitle, title) {
				t.Errorf("w=%d range=%d: missing range %q in panel title %q", w, r, title, panelTitle)
			}
			if tw, budget := ansi.StringWidth(panelTitle), w-10; tw > budget {
				t.Errorf("w=%d range=%d: panel title is %d cells, past the border's %d: %q",
					w, r, tw, budget, panelTitle)
			}
			out := m.renderStatsDetail()
			for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
				if cw := ansi.StringWidth(line); cw > w-8 {
					t.Errorf("w=%d range=%d: line %d cells exceeds inner width %d: %q",
						w, r, cw, w-8, line)
				}
			}
		}
	}

	// The 6-month view labels the axis with week numbers (wNN).
	weekLabel := regexp.MustCompile(`w\d`)
	wide := newTagModel(todos...)
	wide.termWidth = 120
	wide.tab = tabStats
	wide.statsRange = statsRange6Months
	wide.refreshCaches()
	wideOut := wide.renderStatsDetail()
	if !weekLabel.MatchString(wideOut) {
		t.Errorf("expected week-number axis labels in 6-month view, got:\n%s", wideOut)
	}
	// On a wide terminal the whole caption fits on the border.
	if got := wide.statsPanelTitle(); !strings.Contains(got, "1 block = 1 completed task") {
		t.Errorf("expected the legend in the panel title, got: %q", got)
	}

	// The 30-day view draws dotted separators between weeks.
	d30 := newTagModel(todos...)
	d30.termWidth = 120
	d30.tab = tabStats
	d30.statsRange = statsRange30Days
	d30.refreshCaches()
	if out := d30.renderStatsDetail(); !strings.Contains(out, "·") {
		t.Errorf("expected dotted week separators in 30-day view, got:\n%s", out)
	}

	// Empty state.
	empty := newTagModel()
	empty.termWidth = 100
	empty.tab = tabStats
	empty.refreshCaches()
	if out := empty.renderStatsDetail(); !strings.Contains(out, "No completions") {
		t.Errorf("expected empty-state message, got:\n%s", out)
	}
}

// The caption used to be a row of its own inside the pane. On the border it
// costs nothing and gives the chart a row back — but it has to survive a
// narrow window by shedding its least useful half rather than being cut
// mid-word by the border's own truncation.
func TestStatsPanelTitleCarriesTheCaption(t *testing.T) {
	t.Cleanup(func() { applyLang(string(langEN)) })
	now := time.Now()
	var todos []todo.Todo
	for i := 0; i < 4; i++ {
		td := todo.New(fmt.Sprintf("done %d", i))
		td.Status = todo.Done
		td.CompletedAt = now
		todos = append(todos, td)
	}

	for _, lang := range availableLanguages {
		var lastW int
		for _, w := range []int{200, 140, 100, 80, 60, 44, 30, 20, 12} {
			m := newTagModel(todos...)
			// After the model: initialModel applies the stored language, which
			// would put this sweep back into English on every iteration.
			applyLang(string(lang))
			m.termWidth, m.termHeight = w, 30
			m.tab = tabStats
			m.statsRange = statsRange7Days
			m.refreshCaches()

			got := m.statsPanelTitle()
			// It must fit the border's budget on its own; relying on
			// withBorderTitle's "…" would cut the legend mid-sentence.
			if tw, budget := ansi.StringWidth(got), w-10; tw > budget && got != tr("Activity") {
				t.Errorf("lang=%s w=%d: title is %d cells, past the border's %d: %q",
					lang, w, tw, budget, got)
			}
			// Never grows as the window shrinks: the ladder only sheds.
			if tw := ansi.StringWidth(got); lastW != 0 && tw > lastW {
				t.Errorf("lang=%s w=%d: title grew from %d to %d cells as the window narrowed: %q",
					lang, w, lastW, tw, got)
			}
			lastW = ansi.StringWidth(got)

			if w >= 140 {
				for _, want := range []string{tr("Activity"), tr("Last 7 days"), "4", tr("1 block = 1 completed task")} {
					if !strings.Contains(got, want) {
						t.Errorf("lang=%s w=%d: title %q is missing %q", lang, w, got, want)
					}
				}
			}
			// The range survives well past the point the legend has to go —
			// it is the one thing the chart cannot say for itself.
			if w >= 60 && !strings.Contains(got, tr("Last 7 days")) {
				t.Errorf("lang=%s w=%d: title %q dropped the range too early", lang, w, got)
			}
			// And nothing is shed while there was still room for it: the pane's
			// own name goes only once the width it needs is genuinely gone.
			named := ansi.StringWidth(tr("Activity")) + 2
			if lastW+named <= w-10 && !strings.Contains(got, tr("Activity")) {
				t.Errorf("lang=%s w=%d: title %q dropped the pane name with %d cells to spare",
					lang, w, got, w-10-lastW)
			}
		}
	}
}

// Rows above the tallest bar are blank, and a fixed-height chart spends the
// pane's height on them — so the chart draws only as tall as its busiest
// bucket, and the stats list keeps the rest. A quiet week still gets a floor,
// and a busy one still stops at the ceiling.
func TestStatsChartFitsItsTallestBar(t *testing.T) {
	mk := func(counts ...int) []statsBucket {
		out := make([]statsBucket, len(counts))
		for i, c := range counts {
			out[i] = statsBucket{count: c}
		}
		return out
	}
	for _, c := range []struct {
		name   string
		budget int
		bucks  []statsBucket
		want   int
	}{
		{"fits the tallest bar", 12, mk(3, 7, 0, 9), 9},
		{"never below the floor", 12, mk(1, 0, 2), statsChartMinH},
		{"never past the budget", 8, mk(3, 40), 8},
		{"exactly the budget", 8, mk(8), 8},
		{"no completions at all", 12, mk(0, 0), statsChartMinH},
	} {
		if got := statsChartRows(c.budget, c.bucks); got != c.want {
			t.Errorf("%s: statsChartRows = %d, want %d", c.name, got, c.want)
		}
	}

	// End to end: a day inside the budget draws one full block per task, which
	// is the whole point — no half-blocks, and no rows of empty chart above it.
	now := time.Now()
	var todos []todo.Todo
	for i := 0; i < 9; i++ {
		td := todo.New(fmt.Sprintf("done %d", i))
		td.Status = todo.Done
		td.CompletedAt = now
		todos = append(todos, td)
	}
	m := newTagModel(todos...)
	m.termWidth, m.termHeight = 120, 44
	m.tab = tabStats
	m.statsRange = statsRange7Days
	m.refreshCaches()
	out := m.renderStatsDetail()
	if strings.ContainsAny(out, "▀▄") || strings.Contains(out, "+") {
		t.Errorf("9 tasks inside a %d-row budget should be 9 whole blocks, got:\n%s", m.statsChartHeight(), out)
	}
	if rows := strings.Count(strings.TrimRight(out, "\n"), "\n") + 1; rows != 9+2 {
		t.Errorf("chart is %d rows, want 9 bars + baseline + labels", rows)
	}
}

// When a single bucket exceeds chartH, the histogram doubles its scale so a
// busy day collapses to half-height rather than instantly capping with `+`.
// Odd counts render with a half-block (▄) on top so 9 stays visibly shorter
// than 10; only past 2×chartH does `+` re-appear.
func TestStatsHistogramScalesBeforeOverflow(t *testing.T) {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 9, 0, 0, 0, now.Location())
	mk := func(n int) []todo.Todo {
		var todos []todo.Todo
		for i := 0; i < n; i++ {
			td := todo.New(fmt.Sprintf("done %d", i))
			td.Status = todo.Done
			td.CompletedAt = today
			todos = append(todos, td)
		}
		return todos
	}
	render := func(todos []todo.Todo) string {
		m := newTagModel(todos...)
		m.termWidth = 100
		m.termHeight = 30
		m.tab = tabStats
		m.statsRange = statsRange7Days
		m.refreshCaches()
		return m.renderStatsDetail()
	}
	chartH := func() int {
		m := newTagModel()
		m.termWidth, m.termHeight = 100, 30
		return m.statsChartHeight()
	}()

	// 9 tasks at scale=2 → 4 paired-half rows (▀) plus a ▄ cap on the 5th
	// row (the 9th task), no `+`.
	out := render(mk(9))
	if !strings.Contains(out, "▄") {
		t.Errorf("9 tasks should render ▄ for the odd top task, got:\n%s", out)
	}
	if !strings.Contains(out, "▀") {
		t.Errorf("9 tasks should render ▀ for stacked-half-block rows, got:\n%s", out)
	}
	if strings.Contains(out, "+") {
		t.Errorf("scale=2 should fit 9 tasks without `+`, got:\n%s", out)
	}

	// 10 tasks: 5 full ▀-rows (top/bottom both filled), still no `+`.
	out10 := render(mk(10))
	if strings.Contains(out10, "+") {
		t.Errorf("10 tasks should fit without `+`, got:\n%s", out10)
	}
	if strings.Contains(out10, "▄") {
		t.Errorf("10 tasks is even, no orphan ▄, got:\n%s", out10)
	}

	// Past twice the chart height even the half-blocks run out, so `+`
	// reappears. Derived from statsChartHeight rather than a constant, so
	// giving the chart more rows moves the threshold instead of the test.
	over := 2*chartH + 1
	out2 := render(mk(over))
	if !strings.Contains(out2, "+") {
		t.Errorf("%d tasks should overflow scale=2, expected `+`, got:\n%s", over, out2)
	}
}

// Regression: tasks stored UTC (as SQLite does) and rendered with a frameTime
// in a +N timezone were being skipped from today's bucket because the bucket
// day was reconstructed from CompletedAt's UTC date parts, putting "midnight"
// past local midnight and tripping the `d.After(today)` guard.
func TestStatsHistogramTimezoneBucket(t *testing.T) {
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Skip("Asia/Tokyo tzdata not available: " + err.Error())
	}
	td := todo.New("done in Tokyo")
	td.Status = todo.Done
	td.CompletedAt = time.Date(2026, 6, 18, 10, 0, 0, 0, tokyo).UTC()

	m := newTagModel(td)
	m.termWidth = 100
	m.termHeight = 44
	m.tab = tabStats
	m.statsRange = statsRange7Days
	m.refreshCaches()
	m.frameTime = time.Date(2026, 6, 18, 12, 0, 0, 0, tokyo)

	out := m.renderStatsDetail()
	if strings.Contains(out, "No completions in this range") {
		t.Fatalf("today's completion (stored UTC) dropped from 7-day window:\n%s", out)
	}
	if got := m.statsPanelTitle(); !strings.Contains(got, "1 done") {
		t.Errorf("expected '1 done' in the panel title, got: %q", got)
	}
}

// TestStatsHistogramOverflow checks that a bar with more tasks than the height
// limit is capped with a "+" overflow marker.
func TestStatsHistogramOverflow(t *testing.T) {
	now := time.Now()
	var todos []todo.Todo
	for i := 0; i < 30; i++ { // 30 completions, all today — well past any limit
		td := todo.New("x")
		td.Status = todo.Done
		td.CompletedAt = now
		todos = append(todos, td)
	}
	m := newTagModel(todos...)
	m.termWidth = 100
	m.termHeight = 30
	m.tab = tabStats
	m.statsRange = statsRange7Days
	m.refreshCaches()
	if out := m.renderStatsDetail(); !strings.Contains(out, "+") {
		t.Errorf("expected a '+' overflow marker for an over-limit bar, got:\n%s", out)
	}
}

// TestStatsWeekdayLabels checks the 7-day histogram spells weekday names out
// when wide and shrinks them when narrow.
func TestStatsWeekdayLabels(t *testing.T) {
	now := time.Now()
	var todos []todo.Todo
	for d := 0; d < 7; d++ {
		td := todo.New("x")
		td.Status = todo.Done
		td.CompletedAt = now.AddDate(0, 0, -d)
		todos = append(todos, td)
	}
	weekdays := []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}

	wide := newTagModel(todos...)
	wide.termWidth = 110
	wide.termHeight = 40
	wide.tab = tabStats
	wide.statsRange = statsRange7Days
	wide.refreshCaches()
	outWide := wide.renderStatsDetail()
	full := false
	for _, d := range weekdays {
		if strings.Contains(outWide, d) {
			full = true
		}
	}
	if !full {
		t.Errorf("expected full weekday names on a wide 7-day histogram:\n%s", outWide)
	}

	narrow := newTagModel(todos...)
	narrow.termWidth = 44
	narrow.termHeight = 40
	narrow.tab = tabStats
	narrow.statsRange = statsRange7Days
	narrow.refreshCaches()
	outNarrow := narrow.renderStatsDetail()
	for _, d := range weekdays {
		if strings.Contains(outNarrow, d) {
			t.Errorf("did not expect full weekday name %q on a narrow histogram:\n%s", d, outNarrow)
		}
	}
}

// TestStatsRangeCycles confirms Enter on the Stats tab advances the range and
// wraps back to the start.
func TestStatsRangeCycles(t *testing.T) {
	m := newTagModel()
	m.tab = tabStats
	want := []statsRangeMode{statsRange30Days, statsRange6Months, statsRange7Days}
	for i, exp := range want {
		nm, _ := m.handleListEnter()
		m = nm.(model)
		if m.statsRange != exp {
			t.Fatalf("after %d Enter(s): statsRange = %d, want %d", i+1, m.statsRange, exp)
		}
	}
}

// TestStatsNarrowNoWrap renders the whole Stats page across widths and asserts no
// line wraps the terminal — covering both the two-column and fallback paths.
func TestStatsNarrowNoWrap(t *testing.T) {
	todos := statsTodos()
	for _, w := range []int{60, 84, 100, 140} {
		m := newTagModel(todos...)
		m.termWidth = w
		m.termHeight = 40
		m.tab = tabStats
		m.refreshCaches()
		for n, line := range strings.Split(m.View(), "\n") {
			if cw := ansi.StringWidth(line); cw > w {
				t.Errorf("width=%d: line %d is %d cells wide: %q", w, n, cw, line)
			}
		}
	}
}

// TestStatsRespectsSearchFilter pins the Stats-tab scoping contract: an active
// search query (set via / on the Tasks, Projects, or Stats tab) narrows every
// stat block to the matching top-level tasks.
func TestStatsRespectsSearchFilter(t *testing.T) {
	now := time.Now()
	mk := func(title string, tags ...string) todo.Todo {
		td := todo.New(title)
		td.CreatedAt = now.AddDate(0, 0, -1)
		td.Tags = tags
		return td
	}
	tagged1 := mk("tagged one", "work")
	tagged2 := mk("tagged two", "work")
	plain1 := mk("plain one")
	plain2 := mk("plain two")
	doneTagged := mk("tagged done", "work")
	doneTagged.Status = todo.Done
	doneTagged.CompletedAt = now

	m := newTagModel(tagged1, tagged2, plain1, plain2, doneTagged)
	m.termWidth = 120
	m.tab = tabStats
	m.frameTime = now

	activeTotal := regexp.MustCompile(`Active total\s+(\d+)`)

	plainOut := ansi.Strip(m.renderStatsList())
	if got := activeTotal.FindStringSubmatch(plainOut); got == nil || got[1] != "4" {
		t.Fatalf("unfiltered Active total = %v, want 4\n%s", got, plainOut)
	}

	m.searchQuery = "#work"
	filteredOut := ansi.Strip(m.renderStatsList())
	if got := activeTotal.FindStringSubmatch(filteredOut); got == nil || got[1] != "2" {
		t.Fatalf("filtered Active total = %v, want 2\n%s", got, filteredOut)
	}
	if !regexp.MustCompile(`Today\s+1`).MatchString(filteredOut) {
		t.Errorf("filtered velocity should still count the tagged completion:\n%s", filteredOut)
	}
}

// Every bar of the 26-week histogram carries its week number, at every width:
// "w42" when the bars are wide, the bare number when they are narrow, and on
// one-column bars alternating between two label rows so neighbours stay apart.
func TestStatsWeeklyHistogramNumbersEveryWeek(t *testing.T) {
	now := time.Now()
	var todos []todo.Todo
	for i := 0; i < 60; i++ {
		td := todo.New(fmt.Sprintf("t%d", i))
		td.Status = todo.Done
		td.CompletedAt = now.AddDate(0, 0, -i*3)
		todos = append(todos, td)
	}
	for _, w := range []int{60, 80, 100, 120, 160} {
		m := newTagModel(todos...)
		m.termWidth, m.termHeight = w, 40
		m.tab = tabStats
		m.statsRange = statsRange6Months
		m.refreshCaches()
		_, buckets, _, _ := m.statsActivity()
		out := ansi.Strip(m.renderStatsDetail())
		labels := map[string]bool{}
		for _, f := range strings.Fields(out[strings.LastIndex(out, "─")+len("─"):]) {
			labels[strings.TrimPrefix(f, "w")] = true
		}
		// The narrowest widths show only the most recent weeks that fit.
		shown := 0
		for k := len(buckets) - 1; k >= 0; k-- {
			_, wk := buckets[k].start.ISOWeek()
			if !labels[strconv.Itoa(wk)] {
				break
			}
			shown++
		}
		if shown < min(len(buckets), 20) {
			t.Errorf("width %d: only the last %d weeks are numbered:\n%s", w, shown, out)
		}
		for _, line := range strings.Split(out, "\n") {
			if lw := ansi.StringWidth(line); lw > w-8 {
				t.Errorf("width %d: line is %d wide, over %d: %q", w, lw, w-8, line)
			}
		}
	}
}

// A weekday label is placed cell by cell. Indexing it by byte put a hole after
// every non-ASCII letter: "Lø rdag", "Sø ndag" on a Danish install.
func TestStatsWeekdayLabelsKeepNonASCIILettersTogether(t *testing.T) {
	now := time.Now()
	var todos []todo.Todo
	for d := 0; d < 7; d++ {
		td := todo.New("x")
		td.Status = todo.Done
		td.CompletedAt = now.AddDate(0, 0, -d)
		todos = append(todos, td)
	}
	m := newTagModel(todos...)
	applyLang(string(langDA))
	defer applyLang(string(langEN))
	m.termWidth = 110
	m.termHeight = 40
	m.tab = tabStats
	m.statsRange = statsRange7Days
	m.refreshCaches()
	out := ansi.Strip(m.renderStatsDetail())
	for _, day := range []string{"Lørdag", "Søndag"} {
		if !strings.Contains(out, day) {
			t.Errorf("the chart should spell %q whole:\n%s", day, out)
		}
	}
}

// The summary is taller than a small window leaves it. ↓ scrolls it, a marker
// says how much is out of view, and the scroll stops with the last line on
// screen rather than scrolling into blank rows.
func TestStatsSummaryScrollsToItsLastLine(t *testing.T) {
	m := newTagModel(statsTodos()...)
	m.termWidth = 80
	m.termHeight = 24
	m.tab = tabStats
	m.refreshCaches()

	summary := trimTrailingBlank(m.buildListLines())
	last := strings.TrimSpace(ansi.Strip(summary[len(summary)-1]))
	if m.statsMaxScroll() == 0 {
		t.Fatalf("a %d-line summary should not fit 80×24; the test needs a taller one", len(summary))
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "↓") || strings.Contains(view, last) {
		t.Fatalf("unscrolled, the summary should end in a ↓ marker, not its last line:\n%s", view)
	}
	for i := 0; i < len(summary)+5; i++ {
		m = sendKey(t, m, "down")
	}
	if m.statsScroll != m.statsMaxScroll() {
		t.Errorf("scroll = %d after holding ↓, want it to stop at %d", m.statsScroll, m.statsMaxScroll())
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, last) {
		t.Errorf("scrolled to the end, the summary's last line %q should be on screen:\n%s", last, view)
	}
	if !strings.Contains(view, "↑") {
		t.Errorf("scrolled down, a ↑ marker should count the lines above:\n%s", view)
	}
	m = sendKey(t, m, "up")
	if m.statsScroll != m.statsMaxScroll()-1 {
		t.Errorf("one ↑ from the end should move one line, scroll = %d", m.statsScroll)
	}
}

// Each share of a breakdown is rounded: 6 of 9 is 67%, not 66%.
func TestStatsPercentagesRound(t *testing.T) {
	var todos []todo.Todo
	for i, p := range []todo.Priority{todo.PriorityHigh, todo.PriorityHigh, todo.PriorityLow} {
		td := todo.New(fmt.Sprintf("task %d", i))
		td.Priority = p
		todos = append(todos, td)
	}
	for i := 0; i < 6; i++ {
		todos = append(todos, todo.New(fmt.Sprintf("medium %d", i)))
	}
	m := newTagModel(todos...)
	m.termWidth = 140
	m.termHeight = 60
	m.tab = tabStats
	m.refreshCaches()
	out := ansi.Strip(m.renderStatsList())
	if !strings.Contains(out, "67%") || strings.Contains(out, "66%") {
		t.Errorf("6 of 9 should read 67%%:\n%s", out)
	}
}

// A window too short for the chart and the summary both gives the height to
// the summary: the Activity panel goes, and enter, which only changes the
// chart's range, leaves the footer.
func TestStatsShortWindowDropsTheChart(t *testing.T) {
	m := newTagModel(statsTodos()...)
	m.termWidth = 80
	m.tab = tabStats

	m.termHeight = statsChartMinTermH
	if view := ansi.Strip(m.View()); !strings.Contains(view, tr("Activity")) {
		t.Fatalf("at %d rows the chart should still be drawn:\n%s", m.termHeight, view)
	}
	m.termHeight = statsChartMinTermH - 1
	view := ansi.Strip(m.View())
	if strings.Contains(view, tr("Activity")) {
		t.Errorf("at %d rows the chart should give way to the summary:\n%s", m.termHeight, view)
	}
	if strings.Contains(view, "cycle activity range") {
		t.Errorf("with no chart the footer should not offer its range key:\n%s", view)
	}
	before := m.statsRange
	m = sendKey(t, m, "enter")
	if m.statsRange != before {
		t.Error("enter should not change a range that is not on screen")
	}
}

// On a tall window the summary ends well above its panel's edge, and the
// chart takes those rows: each task's block grows taller, and the summary
// still shows whole, without scrolling.
func TestStatsChartGrowsIntoRowsTheSummaryLeaves(t *testing.T) {
	now := time.Now()
	var todos []todo.Todo
	for i := 0; i < 3; i++ {
		td := todo.New(fmt.Sprintf("done %d", i))
		td.Status = todo.Done
		td.CompletedAt = now
		todos = append(todos, td)
	}
	chartRows := func(h int) (rows, maxScroll int) {
		m := newTagModel(todos...)
		m.termWidth, m.termHeight = 120, h
		m.tab = tabStats
		m.statsRange = statsRange7Days
		m.refreshCaches()
		for _, line := range strings.Split(m.View(), "\n") {
			if w := ansi.StringWidth(line); w > m.termWidth {
				t.Fatalf("height %d: line is %d wide, window is %d", h, w, m.termWidth)
			}
		}
		out := strings.TrimRight(m.renderStatsDetail(), "\n")
		return strings.Count(out, "\n") + 1 - 2, m.statsMaxScroll() // less baseline and labels
	}
	if rows, _ := chartRows(34); rows != 3 {
		t.Errorf("a window with no rows to spare should draw one row per task: %d rows for 3 tasks", rows)
	}
	rows, scroll := chartRows(70)
	if rows <= 3 || rows%3 != 0 {
		t.Errorf("a tall window should give each of 3 tasks the same several rows, got %d rows", rows)
	}
	if scroll != 0 {
		t.Errorf("growing the chart should not push the summary into scrolling, max scroll %d", scroll)
	}
}
