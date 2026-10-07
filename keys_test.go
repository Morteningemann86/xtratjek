package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Morteningemann86/xtratjek/todo"
)

// withKeys applies a keybinding overlay for one test, restoring the previous
// one after — the applyTheme/applyLang global pattern.
func withKeys(t *testing.T, overrides map[string]string) {
	t.Helper()
	prev := activeKeys
	clean, problems := sanitizeKeyOverrides(overrides)
	if len(problems) > 0 {
		t.Fatalf("overrides were rejected: %v", problems)
	}
	applyKeys(clean)
	t.Cleanup(func() { activeKeys = prev })
}

// The point of the whole thing: a rebound key performs the action, driven
// through the real dispatch.
func TestRebindingMovesAnAction(t *testing.T) {
	task := todo.New("Alpha")
	m := modelWithTasks(t, task)
	withKeys(t, map[string]string{"done": "E"})

	m = sendKey(t, m, "E")
	if got := m.get(task.ID); got == nil || got.Status != todo.Done {
		t.Fatalf("the rebound key did not complete the task: %v", got)
	}
}

// …and the key it moved off stops working. Otherwise a rebind is an alias, and
// the freed key can never be reused for anything else.
func TestRebindingFreesTheOldKey(t *testing.T) {
	task := todo.New("Alpha")
	m := modelWithTasks(t, task)
	withKeys(t, map[string]string{"done": "E"})

	m = sendKey(t, m, "d")
	if got := m.get(task.ID); got == nil || got.Status != todo.Pending {
		t.Errorf("the old key still completed the task: %v", got)
	}
}

// Keys the registry doesn't own must pass through untouched — most of the
// keyboard is text, and modals own their own keys.
func TestRebindingLeavesOtherKeysAlone(t *testing.T) {
	task := todo.New("Alpha")
	m := modelWithTasks(t, task)
	withKeys(t, map[string]string{"done": "E"})

	m = sendKey(t, m, "a") // still opens quick-add
	if m.mode != modeInput {
		t.Fatalf("an unrelated key was swallowed: mode = %v", m.mode)
	}
	// Typing inside a modal must not be translated: with done→E, a literal "E"
	// belongs in the text.
	m = script(t, m, "Easter plan")
	if got := m.textInput.Value(); !strings.HasPrefix(got, "E") {
		t.Errorf("input = %q, want the typed E to survive", got)
	}
}

// Every surface that shows a key has to show the one that works.
func TestRebindingShowsUpEverywhere(t *testing.T) {
	m := modelWithTasks(t, todo.New("Alpha"))
	withKeys(t, map[string]string{"done": "E"})

	hint := hintString(ctxTasksList, false, nil)
	if !strings.Contains(hint, "E toggle done") {
		t.Errorf("footer hint = %q, want the rebound key", hint)
	}
	help := strings.Join(m.helpBodyLines(), "\n")
	if !strings.Contains(help, "E ") || strings.Contains(help, "\nd  ") {
		t.Errorf("the help overlay still shows the default key")
	}
	var found bool
	for _, c := range paletteModel().paletteCommands() {
		if c.label == "toggle done" && c.tab == tabTasks {
			found = true
			if c.key != "E" {
				t.Errorf("palette shows %q for a rebound action", c.key)
			}
		}
	}
	if !found {
		t.Error("the palette lost the rebound action")
	}
}

// A palette entry runs by pressing its key, so it has to press the effective
// one — pressing the default would be swallowed as a freed key.
func TestPaletteRunsRebound(t *testing.T) {
	task := todo.New("Alpha")
	m := modelWithTasks(t, task)
	withKeys(t, map[string]string{"done": "E"})

	for _, c := range paletteModel().paletteCommands() {
		if c.label == "toggle done" && c.tab == tabTasks {
			next, _ := m.runPaletteCommand(c)
			m = next.(model)
		}
	}
	if got := m.get(task.ID); got == nil || got.Status != todo.Done {
		t.Errorf("running the rebound action from the palette did nothing: %v", got)
	}
}

func TestSanitizeKeyOverrides(t *testing.T) {
	cases := []struct {
		name    string
		in      map[string]string
		want    map[string]string
		problem string
	}{
		{"a plain rebind", map[string]string{"done": "E"}, map[string]string{"done": "E"}, ""},
		{"unknown action", map[string]string{"frobnicate": "z"}, nil, "not a rebindable action"},
		{"empty key", map[string]string{"done": ""}, nil, "has no key"},
		{"multi-key form", map[string]string{"done": "ctrl+shift+x"}, nil, "not a single key"},
		{"ctrl+c is reserved", map[string]string{"done": "ctrl+c"}, nil, "reserved"},
		{"a no-op override is dropped", map[string]string{"done": "d"}, nil, ""},
		{
			"a collision is rejected rather than guessed",
			map[string]string{"done": "t"}, // t is already the timer on the Tasks tab
			nil,
			"already taken",
		},
		{
			"an action bound to a pair cannot be rebound",
			map[string]string{"boardmove": "z"}, nil, "not a rebindable action",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, problems := sanitizeKeyOverrides(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("overrides = %v, want %v", got, c.want)
			}
			joined := strings.Join(problems, "; ")
			if c.problem == "" {
				if joined != "" {
					t.Errorf("unexpected problem: %s", joined)
				}
			} else if !strings.Contains(joined, c.problem) {
				t.Errorf("problems = %q, want one mentioning %q", joined, c.problem)
			}
		})
	}
}

// Swapping two actions is the case a naive implementation gets wrong: each key
// must land on the action the user asked for, not on the other one.
func TestRebindingCanSwapTwoActions(t *testing.T) {
	// initialModel applies the stored overlay, so build every model first — the
	// same ordering trap the theme and language globals have.
	task, other := todo.New("Alpha"), todo.New("Beta")
	m := modelWithTasks(t, task)
	m2 := modelWithTasks(t, other)
	withKeys(t, map[string]string{"done": "t", "track": "d"})
	m = sendKey(t, m, "t")
	if got := m.get(task.ID); got == nil || got.Status != todo.Done {
		t.Errorf("t did not complete the task after the swap: %v", got)
	}
	// The other half of the swap needs its own model: the task just completed
	// left the active list, so there is nothing under the cursor to time.
	m2 = sendKey(t, m2, "d")
	if got := m2.get(other.ID); got == nil || !got.IsTimerRunning() {
		t.Errorf("d did not start the timer after the swap: %v", got)
	}
	if got := m2.get(other.ID); got != nil && got.Status == todo.Done {
		t.Error("d completed the task instead of starting its timer")
	}
}

// The overlay survives a settings round trip, or a rebind would vanish on the
// next preference change.
func TestKeyOverridesPersist(t *testing.T) {
	restoreSettingsFile(t)
	m := modelWithTasks(t, todo.New("Alpha"))
	withKeys(t, map[string]string{"done": "E"})
	m.persistSettings()

	loaded, err := loadSettings()
	if err != nil {
		t.Fatalf("loadSettings: %v", err)
	}
	if loaded.Keys["done"] != "E" {
		t.Errorf("saved keys = %v, want the rebind", loaded.Keys)
	}
}
