package main

import (
	"strings"
	"testing"

	"github.com/Morteningemann86/xtratjek/todo"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// boardWithOneCard is a Board with a single Backlog card, focused.
func boardWithOneCard(t *testing.T) (model, string) {
	t.Helper()
	card := todo.New("Draft the budget")
	m := newTagModel(card)
	m.tab = tabBoard
	m.termWidth, m.termHeight = 120, 30
	m.refreshCaches()
	return m, card.ID
}

// A card carried over Done previews the landing with a ✓, and the lit frame
// still keeps the no-wrap contract.
func TestBoardCarryOverDoneShowsACheck(t *testing.T) {
	before := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(before)

	m, id := boardWithOneCard(t)
	keys := []string{"enter"}
	for i := 0; i < m.boardCfg.doneColumn(); i++ {
		keys = append(keys, "right")
	}
	m = script(t, m, keys...)
	if !m.carrying(id) || !m.carryGlowDone() {
		t.Fatalf("the card should be held over Done: mode %v col %d", m.mode, m.board.carryCol)
	}
	out := m.View()
	if !strings.Contains(ansi.Strip(out), "✓ Draft the budget") {
		t.Fatalf("a card held over Done should show a ✓:\n%s", ansi.Strip(out))
	}
	for _, line := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(line); w > m.termWidth {
			t.Fatalf("carry frame line is %d wide, window is %d", w, m.termWidth)
		}
	}

	m = script(t, m, "enter")
	if m.carryGlowDone() || m.get(id).Status != todo.Done {
		t.Fatalf("putting it down should close it and end the carry: mode %v", m.mode)
	}
	if strings.Contains(ansi.Strip(m.View()), "✓ Draft the budget") {
		t.Fatal("the ✓ is a carry preview and should go once the card is down")
	}
}

// The selected card's title takes its border's colour, so the whole card
// lights up and not only its frame; the other cards' titles stay plain.
func TestBoardSelectedCardTextLightsUp(t *testing.T) {
	before := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer func() {
		lipgloss.SetColorProfile(before)
		applyTheme(themes[0])
	}()
	applyTheme(themes[0])

	card := todo.New("Draft the budget")
	m := newTagModel(card)
	lit := lipgloss.NewStyle().Foreground(currentTheme.green).Bold(true).Render("Draft the budget")
	if got := strings.Join(m.renderBoardBox(m.get(card.ID), false, true, 30, 2), "\n"); !strings.Contains(got, lit) {
		t.Errorf("selected card title is not lit in the selection colour:\n%q", got)
	}
	if got := strings.Join(m.renderBoardBox(m.get(card.ID), false, false, 30, 2), "\n"); strings.Contains(got, lit) {
		t.Errorf("an unselected card's title should not be lit:\n%q", got)
	}
}

// A card picked up below the top of its column is lifted to the top at once,
// where the carry cursor sits, so the cursor never marks a card left behind.
func TestBoardCarryMarksTheHeldCardInItsOwnColumn(t *testing.T) {
	top, below := todo.New("Top card"), todo.New("Lower card")
	m := modelWithTasks(t, top, below)
	m = script(t, m, "5")
	cols := m.boardColumns()
	col, _ := m.boardSelection(cols)
	if len(cols[col]) < 2 {
		t.Fatalf("want two cards in the focused column, got %d", len(cols[col]))
	}
	held := cols[col][1].ID
	m = script(t, m, "down", "enter")
	if !m.carrying(held) {
		t.Fatalf("enter should pick up the second card: mode %v carry %q", m.mode, m.board.carryID)
	}
	view := m.boardColumnsForView()
	vcol, vcursor := m.boardSelection(view)
	if got := view[vcol][vcursor].ID; got != held {
		t.Errorf("the carry cursor marks %q, want the held card %q", m.get(got).Title, m.get(held).Title)
	}
	if n := len(view[vcol]); n != len(cols[col]) {
		t.Errorf("lifting the card changed the column's count: %d, want %d", n, len(cols[col]))
	}
}

// A held card is marked by its lit frame alone; the cursor's ▶ beside it read
// as a second selection.
func TestBoardHeldCardHasNoCursorArrow(t *testing.T) {
	m, _ := boardWithOneCard(t)
	if !strings.Contains(ansi.Strip(m.View()), cursorMark) {
		t.Fatal("setup: the selected card should carry the ▶ before it is picked up")
	}
	m = script(t, m, "enter")
	if strings.Contains(ansi.Strip(m.renderBoardList()), strings.TrimSpace(cursorMark)) {
		t.Errorf("a held card should have no ▶:\n%s", ansi.Strip(m.renderBoardList()))
	}
	m = script(t, m, "right")
	if strings.Contains(ansi.Strip(m.renderBoardList()), strings.TrimSpace(cursorMark)) {
		t.Errorf("a card carried to the next column should have no ▶ either:\n%s", ansi.Strip(m.renderBoardList()))
	}
}
