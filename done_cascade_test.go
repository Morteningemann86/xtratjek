package main

import (
	"os"
	"strings"
	"testing"

	"github.com/Morteningemann86/xtratjek/todo"
)

// nonTTYStdin swaps os.Stdin for a pipe (not a character device) so stdinIsTTY
// returns false and `done` takes its non-interactive path deterministically,
// regardless of whether the test runs from a terminal or CI.
func nonTTYStdin(t *testing.T) func() {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	w.Close() // EOF; the non-interactive path doesn't read stdin anyway
	orig := os.Stdin
	os.Stdin = r
	return func() { os.Stdin = orig; r.Close() }
}

// addQuietID adds a task via the CLI and returns its full ID. --quiet-id prints
// only the ID, which captureStdout hands back so tests can address the task and
// hang subtasks off it.
func addQuietID(t *testing.T, args ...string) string {
	t.Helper()
	out := captureStdout(t, func() {
		if code := cliAdd(append(args, "--quiet-id")); code != 0 {
			t.Fatalf("cliAdd(%v) = %d, want 0", args, code)
		}
	})
	id := strings.TrimSpace(out)
	if id == "" {
		t.Fatal("cliAdd --quiet-id printed no ID")
	}
	return id
}

func childrenOf(todos []todo.Todo, parentID string) []todo.Todo {
	var out []todo.Todo
	for _, t := range todos {
		if t.ParentID == parentID {
			out = append(out, t)
		}
	}
	return out
}

// TestCliDoneParentWithPendingSubtasks guards e71788f0: closing a parent must
// not silently orphan its pending subtasks. Non-interactively (tests are not a
// TTY) `done` closes the parent only and leaves the subtasks pending; `--cascade`
// closes the whole subtree.
func TestCliDoneParentWithPendingSubtasks(t *testing.T) {
	defer nonTTYStdin(t)()

	pid := addQuietID(t, "cascade-parent-A")
	captureStdout(t, func() {
		if code := cliSubtask([]string{pid, "child-A1"}); code != 0 {
			t.Fatalf("cliSubtask A1 = %d", code)
		}
		if code := cliSubtask([]string{pid, "child-A2"}); code != 0 {
			t.Fatalf("cliSubtask A2 = %d", code)
		}
	})

	// Non-interactive done: parent closes, subtasks stay pending (warning only).
	captureStdout(t, func() {
		if code := cliDone([]string{pid}); code != 0 {
			t.Fatalf("cliDone = %d", code)
		}
	})
	_, todos, err := loadForCLI()
	if err != nil {
		t.Fatalf("loadForCLI: %v", err)
	}
	parent, err := findTaskByRef(todoPtrs(todos), pid)
	if err != nil {
		t.Fatalf("findTaskByRef: %v", err)
	}
	if parent.Status != todo.Done {
		t.Errorf("parent status = %v, want Done", parent.Status)
	}
	kids := childrenOf(todos, pid)
	if len(kids) != 2 {
		t.Fatalf("children = %d, want 2", len(kids))
	}
	for _, k := range kids {
		if k.Status != todo.Pending {
			t.Errorf("subtask %q status = %v, want Pending (no silent cascade)", k.Title, k.Status)
		}
	}

	// --cascade closes the whole subtree.
	pid2 := addQuietID(t, "cascade-parent-B")
	captureStdout(t, func() {
		if code := cliSubtask([]string{pid2, "child-B1"}); code != 0 {
			t.Fatalf("cliSubtask B1 = %d", code)
		}
		if code := cliSubtask([]string{pid2, "child-B2"}); code != 0 {
			t.Fatalf("cliSubtask B2 = %d", code)
		}
	})
	captureStdout(t, func() {
		if code := cliDone([]string{pid2, "--cascade"}); code != 0 {
			t.Fatalf("cliDone --cascade = %d", code)
		}
	})
	_, todos, err = loadForCLI()
	if err != nil {
		t.Fatalf("loadForCLI: %v", err)
	}
	parent2, err := findTaskByRef(todoPtrs(todos), pid2)
	if err != nil {
		t.Fatalf("findTaskByRef parent2: %v", err)
	}
	if parent2.Status != todo.Done {
		t.Errorf("cascaded parent status = %v, want Done", parent2.Status)
	}
	for _, k := range childrenOf(todos, pid2) {
		if k.Status != todo.Done {
			t.Errorf("cascaded subtask %q status = %v, want Done", k.Title, k.Status)
		}
	}
}
