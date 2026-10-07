package main

import (
	"strings"
	"testing"

	"github.com/Morteningemann86/xtratjek/todo"
)

// paletteModel is the smallest model the palette reads: the default board.
func paletteModel() model { return model{boardCfg: defaultBoardConfig()} }

// Every palette entry works by pressing a key, so every entry's key must be one
// the app actually binds on that tab. An entry pressing an unbound key would
// look like a working command and do nothing.
func TestPaletteEntriesPressBoundKeys(t *testing.T) {
	extras := map[string]bool{}
	for _, e := range paletteExtras {
		extras[string(rune(e.tab))+e.key] = true
	}
	for _, c := range paletteModel().paletteCommands() {
		if !paletteSendable(c.key) && !extras[string(rune(c.tab))+c.key] {
			t.Errorf("entry %q presses %q, which is not a single sendable key", c.label, c.key)
		}
		if c.anyTab || extras[string(rune(c.tab))+c.key] {
			continue // globals and the explicit extras are checked by behaviour below
		}
		ctx, ok := paletteCtxFor(c.tab)
		if !ok {
			t.Errorf("entry %q targets tab %d, which has no palette context", c.label, c.tab)
			continue
		}
		bound := false
		for _, bd := range keymap {
			if bd.ctx&ctx != 0 && bd.key == c.key {
				bound = true
				break
			}
		}
		if !bound {
			t.Errorf("entry %q presses %q on tab %d, where nothing binds it", c.label, c.key, c.tab)
		}
	}
}

// paletteCtxFor is the inverse of paletteTabs, for the test above.
func paletteCtxFor(tb tab) (keyCtx, bool) {
	for ctx, mapped := range paletteTabs {
		if mapped == tb {
			return ctx, true
		}
	}
	return 0, false
}

// Running an entry performs the action, switching tabs first when the key
// belongs to another one — which is the point: you find the command by name
// from wherever you happen to be.
func TestPaletteRunsCommands(t *testing.T) {
	find := func(t *testing.T, label string, tb tab) paletteCmd {
		t.Helper()
		for _, c := range paletteModel().paletteCommands() {
			if c.label == label && (c.anyTab || c.tab == tb) {
				return c
			}
		}
		t.Fatalf("no palette entry %q for tab %d", label, tb)
		return paletteCmd{}
	}

	// A tab jump.
	m := modelWithTasks(t, todo.New("Alpha"))
	next, _ := m.runPaletteCommand(find(t, "Go to Board", tabBoard))
	m = next.(model)
	if m.tab != tabBoard {
		t.Errorf("running 'Go to Board' left us on tab %d", m.tab)
	}
	if m.mode != modeNormal {
		t.Errorf("the palette stayed open after running a command: mode %v", m.mode)
	}

	// An action on another tab: from the Board, complete a task on the Tasks tab.
	task := todo.New("Alpha")
	m = modelWithTasks(t, task)
	m.tab = tabBoard
	next, _ = m.runPaletteCommand(find(t, "toggle done", tabTasks))
	m = next.(model)
	if m.tab != tabTasks {
		t.Fatalf("running a Tasks command from the Board did not switch tabs: %d", m.tab)
	}
	if got := m.get(task.ID); got == nil || got.Status != todo.Done {
		t.Errorf("the command did not reach the task: %v", got)
	}

	// One of the explicit extras, whose key the registry only lists as a pair.
	card := todo.New("Alpha")
	m = modelWithTasks(t, card)
	m.tab = tabBoard
	next, _ = m.runPaletteCommand(find(t, "move card to the next stage", tabBoard))
	m = next.(model)
	if got := m.get(card.ID); got == nil || got.Stage == "" {
		t.Errorf("the board move did not change the card's stage: %v", got)
	}
}

// The ranking is what makes the palette usable: the whole query as a substring
// first, then multi-word substrings, and subsequence matching only for
// abbreviation-length queries — otherwise a five-letter query threads through
// unrelated labels and the list fills with noise.
func TestPaletteRanking(t *testing.T) {
	stage := paletteModel().paletteResults("stage")
	if len(stage) == 0 {
		t.Fatal("'stage' found nothing")
	}
	for _, c := range stage {
		if !strings.Contains(strings.ToLower(c.label+c.section), "stage") {
			t.Errorf("'stage' matched %q, which does not contain it", c.label)
		}
	}

	if got := paletteModel().paletteResults("board"); got[0].label != tr("Go to Board") {
		t.Errorf("'board' ranked %q first, want the tab jump", got[0].label)
	}

	// Abbreviations still work at three runes or fewer.
	found := false
	for _, c := range paletteModel().paletteResults("gtb") {
		if c.label == tr("Go to Board") {
			found = true
		}
	}
	if !found {
		t.Error("'gtb' should still find 'Go to Board' as an abbreviation")
	}

	// Multi-word queries AND their words together.
	hits := paletteModel().paletteResults("tag rename")
	if len(hits) == 0 {
		t.Fatal("'tag rename' found nothing")
	}
	for _, c := range hits {
		if !strings.Contains(strings.ToLower(c.label+c.section), "rename") {
			t.Errorf("'tag rename' matched %q", c.label)
		}
	}

	if got := paletteModel().paletteResults("zzzznope"); len(got) != 0 {
		t.Errorf("a nonsense query matched %d commands, want none", len(got))
	}
}

// The palette's own keys, driven through Update.
func TestPaletteKeyFlow(t *testing.T) {
	m := modelWithTasks(t, todo.New("Alpha"))
	m = sendKey(t, m, "ctrl+k")
	if m.mode != modePalette {
		t.Fatalf("ctrl+k did not open the palette: mode %v", m.mode)
	}

	for _, k := range []string{"b", "o", "a", "r", "d"} {
		m = sendKey(t, m, k)
	}
	if got := m.paletteInput.Value(); got != "board" {
		t.Fatalf("query = %q, want %q", got, "board")
	}
	results := m.paletteResults(m.paletteInput.Value())
	if len(results) < 2 {
		t.Fatalf("need at least two results to move between, got %d", len(results))
	}
	m = sendKey(t, m, "down")
	if m.paletteCursor != 1 {
		t.Fatalf("↓ left the cursor at %d", m.paletteCursor)
	}
	m = sendKey(t, m, "backspace")
	if m.paletteInput.Value() != "boar" {
		t.Errorf("backspace left %q", m.paletteInput.Value())
	}
	if m.paletteCursor != 0 {
		t.Errorf("editing the query should re-aim at the top match, cursor = %d", m.paletteCursor)
	}

	m = sendKey(t, m, "esc")
	if m.mode != modeNormal {
		t.Errorf("esc did not close the palette: mode %v", m.mode)
	}
	if m.paletteInput.Value() != "" {
		t.Errorf("the query survived the close: %q", m.paletteInput.Value())
	}
}

// Opening the palette from the detail pane must work too — it is advertised as
// a global binding.
func TestPaletteOpensFromDetailPane(t *testing.T) {
	task := todo.New("Alpha")
	m := modelWithTasks(t, task)
	m = sendKey(t, m, "enter")
	if m.pane != paneDetail {
		t.Fatalf("enter did not open the detail pane: %v", m.pane)
	}
	m = sendKey(t, m, "ctrl+k")
	if m.mode != modePalette {
		t.Errorf("ctrl+k in the detail pane: mode %v, want the palette", m.mode)
	}
}
