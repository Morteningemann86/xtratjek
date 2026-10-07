package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Morteningemann86/xtratjek/todo"
)

func TestComputeLayout(t *testing.T) {
	tests := []struct {
		name        string
		input       layoutInput
		wantDetailH int
	}{
		{
			name: "basic layout",
			input: layoutInput{
				termW:       120,
				termH:       40,
				mode:        modeNormal,
				tab:         tabTasks,
				detailLines: 10,
			},
			wantDetailH: 10,
		},
		{
			name: "stats tab no detail",
			input: layoutInput{
				termW:       120,
				termH:       40,
				mode:        modeNormal,
				tab:         tabStats,
				detailLines: 10,
			},
			wantDetailH: 0,
		},
		{
			name: "input mode no detail",
			input: layoutInput{
				termW:       120,
				termH:       40,
				mode:        modeInput,
				tab:         tabTasks,
				detailLines: 10,
			},
			wantDetailH: 0,
		},
		{
			name: "very small terminal",
			input: layoutInput{
				termW:       40,
				termH:       10,
				mode:        modeNormal,
				tab:         tabTasks,
				detailLines: 20,
			},
		},
		{
			name: "detail capped at max pct",
			input: layoutInput{
				termW:       120,
				termH:       30,
				mode:        modeNormal,
				tab:         tabTasks,
				detailLines: 100,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := computeLayout(tt.input)

			if l.contentW != tt.input.termW-4 {
				t.Errorf("contentW = %d, want %d", l.contentW, tt.input.termW-4)
			}
			if l.listH < minListHeight {
				t.Errorf("listH = %d, below minimum %d", l.listH, minListHeight)
			}
			if tt.wantDetailH > 0 && l.detailH != tt.wantDetailH {
				t.Errorf("detailH = %d, want %d", l.detailH, tt.wantDetailH)
			}

			total := l.headerH + l.listH + l.detailH + l.footerH
			if total > tt.input.termH+minListHeight {
				t.Errorf("total layout %d exceeds terminal height %d (with minList tolerance)",
					total, tt.input.termH)
			}
		})
	}
}

func TestComputeLayoutContentWidth(t *testing.T) {
	l := computeLayout(layoutInput{termW: 100, termH: 40, mode: modeNormal, tab: tabTasks})
	if l.contentW != 96 {
		t.Errorf("contentW = %d, want 96", l.contentW)
	}
}

// The header is now a fixed two rows (tab bar + one status line) — filters and
// toasts render into that single status line instead of stacking their own
// rows, so the header height never varies and the list never reflows.
func TestComputeLayoutHeaderFixed(t *testing.T) {
	l := computeLayout(layoutInput{termW: 100, termH: 40, mode: modeNormal, tab: tabTasks})
	if l.headerH != minHeaderLines {
		t.Errorf("headerH = %d, want fixed %d", l.headerH, minHeaderLines)
	}
}

// When the detail pane is hidden (pane != paneDetail on tabs that can hide
// it), estimateListHeight and listVisible must no longer reserve the 12-line
// detail block — otherwise the task list silently caps at termH-12 instead of
// filling the window. Backlog item bbd963df.
func TestListHeightFillsWhenDetailHidden(t *testing.T) {
	m := modelWithTasks(t, todo.New("a"), todo.New("b"))
	m.termHeight = 40
	// Stacked mode: below the side-by-side threshold the open detail pane
	// steals list rows, so hiding it must grow the list.
	m.termWidth = sideBySideMinWidth - 10

	m.pane = paneDetail
	withDetail := m.estimateListHeight()
	withDetailVisible := m.listVisible()

	m.pane = paneList
	withoutDetail := m.estimateListHeight()
	withoutDetailVisible := m.listVisible()

	if withoutDetail <= withDetail {
		t.Errorf("estimateListHeight: hiding the pane did not grow the list: with=%d without=%d",
			withDetail, withoutDetail)
	}
	if withoutDetailVisible <= withDetailVisible {
		t.Errorf("listVisible: hiding the pane did not grow the list: with=%d without=%d",
			withDetailVisible, withoutDetailVisible)
	}

	// Side-by-side: the detail lives in its own column, so list height must
	// be pane-independent.
	m.termWidth = sideBySideMinWidth
	m.pane = paneDetail
	sbsEstimateWith := m.estimateListHeight()
	sbsWith := m.listVisible()
	m.pane = paneList
	sbsEstimateWithout := m.estimateListHeight()
	sbsWithout := m.listVisible()
	if sbsEstimateWith != sbsEstimateWithout {
		t.Errorf("side-by-side: estimated list height should be pane-independent: focused=%d unfocused=%d",
			sbsEstimateWith, sbsEstimateWithout)
	}
	if sbsWith != sbsWithout {
		t.Errorf("side-by-side: list height should be pane-independent: focused=%d unfocused=%d",
			sbsWith, sbsWithout)
	}
}

func TestSplitStackGivesEachPanelWhatItNeeds(t *testing.T) {
	cases := []struct {
		name                     string
		area, listNeed, paneNeed int
		wantList                 int
	}{
		{"both fit: the list hugs its rows", 40, 8, 20, 8},
		{"few groups, long pane: the pane takes the rest", 40, 8, 90, 8},
		{"many groups, short pane: the list takes the rest", 40, 90, 12, 28},
		{"both overflow: half each", 40, 90, 90, 20},
		{"never below the panel floor", 40, 2, 90, minListPanelLines + detailBorderLines},
	}
	for _, c := range cases {
		if got := splitStack(c.area, c.listNeed, c.paneNeed); got != c.wantList {
			t.Errorf("%s: splitStack(%d, %d, %d) = %d, want %d", c.name, c.area, c.listNeed, c.paneNeed, got, c.wantList)
		}
	}
}

// A few tags with many tasks: the tag list shrinks to its rows and the tasks
// under it get the rest, stacked or beside it.
func TestTagsTabGivesTheRoomToTheTasks(t *testing.T) {
	var ts []todo.Todo
	for i := 0; i < 40; i++ {
		x := todo.New(fmt.Sprintf("Work item %d", i))
		x.AddTag("work")
		ts = append(ts, x)
	}
	home := todo.New("Home item")
	home.AddTag("home")
	ts = append(ts, home)

	for _, w := range []int{90, 120} { // stacked, then side by side
		m := modelWithTasks(t, ts...)
		m.termWidth, m.tab = w, tabTags
		m.clampCursors()
		view := m.View()
		rows := strings.Count(view, "[ ] Work item")
		// 40 rows less the header, key hints, the tag list (two rows plus its
		// header and panel), and the pane's own summary and chrome.
		if rows < 20 {
			t.Errorf("width %d: %d task rows shown in a 40-row window, want at least 20\n%s", w, rows, view)
		}
	}
}
