package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/rank"
	"github.com/Morteningemann86/xtratjek/todo"
	"github.com/charmbracelet/x/ansi"
)

// The reading side of the explanation: the w overlay and the sentences
// view_explain.go builds from the rank package's reason codes.

// explainNow pins the clock for the explanations built here.
var explainNow = time.Date(2026, 6, 17, 14, 0, 0, 0, time.UTC)

// Done tasks score 0 by rule; the explanation must say that instead of
// printing five zeros and a total nobody can act on.
func TestExplainDoneTaskSaysSo(t *testing.T) {
	tt := todo.New("finished")
	tt.Status = todo.Done
	tt.Priority = todo.PriorityHigh
	e := rank.ExplainAt(explainNow, &tt, []*todo.Todo{&tt}, rank.Biases{Aging: true}, rank.Heat{})
	if !e.Done || len(e.Factors) != 0 {
		t.Errorf("done task: Done=%v with %d factors, want the done headline and no breakdown", e.Done, len(e.Factors))
	}
	if !strings.Contains(seqHeadline(e), "done") {
		t.Errorf("headline %q does not mention that the task is done", seqHeadline(e))
	}
}

func explainModel(t *testing.T) model {
	t.Helper()
	overdue := todo.New("Fix the boiler in the basement before the inspection")
	overdue.Priority = todo.PriorityHigh
	overdue.Project = "House"
	overdue.AddTag("home")
	overdue.DueDate = time.Now().Add(-72 * time.Hour)
	soon := todo.New("Write the quarterly memo")
	soon.DueDate = time.Now().Add(48 * time.Hour)
	plain := todo.New("Buy filters")
	return modelWithTasks(t, overdue, soon, plain)
}

// w opens the overlay on the task under the cursor and pins it by ID, so a
// re-sort underneath cannot swap out what is being explained.
func TestWhyKeyOpensTheOverlayOnTheCurrentTask(t *testing.T) {
	m := explainModel(t)
	want := m.currentTodo()
	if want == nil {
		t.Fatal("no task under the cursor")
	}
	m = sendKey(t, m, "w")
	if m.mode != modeExplain {
		t.Fatalf("mode = %v after w, want modeExplain", m.mode)
	}
	if m.explainTaskID != want.ID {
		t.Errorf("overlay pinned %q, want the task under the cursor (%q)", m.explainTaskID, want.ID)
	}
	if body := m.View(); !strings.Contains(body, "Why this rank") {
		t.Error("the overlay did not render its title")
	}
	m = sendKey(t, m, "esc")
	if m.mode != modeNormal || m.explainTaskID != "" {
		t.Errorf("esc left mode=%v pinned=%q, want a closed overlay", m.mode, m.explainTaskID)
	}
}

// The overlay says the same things the CLI prints — one score, one story.
func TestOverlayAndCLIAgree(t *testing.T) {
	m := explainModel(t)
	tt := m.currentTodo()
	e := m.rank.Explain(tt, m.allTodos())

	plain := strings.Join(explainPlainLines(e), "\n")
	if !strings.Contains(plain, tt.Title) {
		t.Errorf("plain output does not name the task:\n%s", plain)
	}
	for _, name := range rank.DimNames {
		if !strings.Contains(plain, name) {
			t.Errorf("plain output is missing the %s row:\n%s", name, plain)
		}
	}
	styled := strings.Join(m.explainBodyLines(e, 100), "\n")
	if !strings.Contains(ansi.Strip(styled), tt.Title) {
		t.Error("the overlay body does not name the task")
	}
}

// The no-wrap contract, swept across widths and languages — German is the
// width stress case, and a translated sentence is exactly what would push a
// hand-budgeted column over the edge.
func TestOverlayHonoursTheWidthBudget(t *testing.T) {
	defer applyLang("en")
	m := explainModel(t)
	e := m.rank.Explain(m.currentTodo(), m.allTodos())
	for _, lang := range availableLanguages {
		applyLang(string(lang))
		for _, w := range []int{0, 1, 8, 20, 40, 60, 80, 100, 160} {
			for _, line := range m.explainBodyLines(e, w) {
				if got := ansi.StringWidth(line); got > w && w >= 8 {
					t.Errorf("%s at width %d: line is %d wide: %q", lang, w, got, ansi.Strip(line))
				}
			}
		}
	}
}

// The overlay must survive being asked about a task that is not in the
// ranking at all — a subtask, or a row whose ID went stale.
func TestOverlayHandlesUnrankedAndMissingTasks(t *testing.T) {
	parent := todo.New("parent")
	parent.ID = "p"
	sub := todo.New("subtask")
	sub.ID = "s"
	sub.ParentID = "p"
	m := modelWithTasks(t, parent, sub)

	e := m.rank.Explain(m.get("s"), m.allTodos())
	if e.Pos != 0 {
		t.Errorf("a subtask reported rank #%d, want unranked", e.Pos)
	}
	if len(m.explainBodyLines(e, 80)) == 0 {
		t.Error("an unranked task rendered no body")
	}

	m.mode = modeExplain
	m.explainTaskID = "gone"
	if got := m.View(); got == "" {
		t.Error("a stale explain ID rendered nothing")
	}
}
