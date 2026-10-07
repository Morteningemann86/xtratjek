package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Morteningemann86/xtratjek/paths"
	"github.com/Morteningemann86/xtratjek/todo"
)

// resetCrashState clears the globals a guard sets, so one test's report is
// never the thing another test asserts on.
func resetCrashState(t *testing.T) {
	t.Helper()
	lastCrashReport, lastCrashSaved = "", false
	t.Cleanup(func() { lastCrashReport, lastCrashSaved = "", false })
}

// TestCrashGuardReportsFromThePanicSiteAndSaves is the whole point of guarding
// inside Update rather than around Run: the report holds the stack of the
// function that panicked, and the edits the save debounce had not yet written
// reach the repository anyway.
func TestCrashGuardReportsFromThePanicSiteAndSaves(t *testing.T) {
	setTestHome(t, t.TempDir())
	resetCrashState(t)

	repo := &fakeRepo{}
	m := initialModel(repo)
	m.termWidth, m.termHeight = 100, 30
	task := m.add(todo.New("typed just before the crash"))
	m.markModified(task.ID)

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("the guard swallowed the panic — Bubble Tea never gets to restore the terminal")
			}
		}()
		defer m.crashGuard("update", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		panic("boom")
	}()

	if lastCrashReport == "" {
		t.Fatal("no crash report path recorded")
	}
	body, err := os.ReadFile(lastCrashReport)
	if err != nil {
		t.Fatalf("read crash report: %v", err)
	}
	report := string(body)

	for _, want := range []string{
		"panic: boom",
		"stage:    update (key j)", // the message is named, so the crash is reproducible
		"terminal: 100x30",
		"tasks=1",
		// The stack was taken while the panicking frames were still live. Taken
		// from main after Run returns instead, this frame would be long gone.
		"TestCrashGuardReportsFromThePanicSiteAndSaves",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("crash report missing %q:\n%s", want, report)
		}
	}

	if !lastCrashSaved {
		t.Error("lastCrashSaved is false, but the flush had nothing to fail on")
	}
	if len(repo.todos) != 1 || repo.todos[0].Title != task.Title {
		t.Errorf("pending edit was not flushed on crash: repo holds %+v", repo.todos)
	}
}

// TestCrashGuardIsInertWithoutAPanic guards the hot path's contract: Update and
// View defer this on every frame, and a frame that does not panic must not
// write anything or touch the globals.
func TestCrashGuardIsInertWithoutAPanic(t *testing.T) {
	setTestHome(t, t.TempDir())
	resetCrashState(t)

	m := newTestModel()
	func() { defer m.crashGuard("update", nil) }()

	if lastCrashReport != "" {
		t.Fatalf("a quiet frame wrote a crash report at %s", lastCrashReport)
	}
	stateDir, err := paths.Dir(paths.State)
	if err != nil {
		t.Fatalf("paths.Dir: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(stateDir, "crash-*.log"))
	if len(matches) > 0 {
		t.Fatalf("unexpected crash reports on disk: %v", matches)
	}
}

// TestCrashReportsPruneToTheNewest keeps a reproducible crash from filling a
// disk one report per launch.
func TestCrashReportsPruneToTheNewest(t *testing.T) {
	dir := t.TempDir()

	var written []string
	for _, stamp := range []string{
		"20260101-120000.000", "20260102-120000.000", "20260103-120000.000",
		"20260104-120000.000", "20260105-120000.000", "20260106-120000.000",
		"20260107-120000.000", "20260108-120000.000",
	} {
		path := filepath.Join(dir, "crash-"+stamp+".log")
		if err := os.WriteFile(path, []byte("report"), 0o600); err != nil {
			t.Fatalf("seed %s: %v", path, err)
		}
		written = append(written, path)
	}
	// An unrelated file in the state directory must survive the prune.
	other := filepath.Join(dir, "trace.log")
	if err := os.WriteFile(other, []byte("trace"), 0o600); err != nil {
		t.Fatalf("seed trace.log: %v", err)
	}

	pruneCrashReports(dir)

	left, err := filepath.Glob(filepath.Join(dir, "crash-*.log"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	sort.Strings(left)
	want := written[len(written)-maxCrashReports:]
	if len(left) != len(want) {
		t.Fatalf("kept %d reports, want %d: %v", len(left), len(want), left)
	}
	for i := range want {
		if left[i] != want[i] {
			t.Errorf("kept %s, want the newer %s", left[i], want[i])
		}
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("prune removed a file that is not a crash report: %v", err)
	}
}
