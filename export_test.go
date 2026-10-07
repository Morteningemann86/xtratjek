package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/rank"
	"github.com/Morteningemann86/xtratjek/todo"
)

// ── parseExportData unit tests ────────────────────────────────────────────────

func TestParseExportDataEnvelope(t *testing.T) {
	tasks := []todo.Todo{todo.New("alpha"), todo.New("beta")}
	env := exportEnvelope{Version: 1, ExportedAt: time.Now().UTC(), Tasks: tasks}
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := parseExportData(data)
	if err != nil {
		t.Fatalf("parseExportData: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 tasks, got %d", len(got))
	}
	if got[0].Title != "Alpha" || got[1].Title != "Beta" {
		t.Errorf("titles = %q, %q; want Alpha, Beta", got[0].Title, got[1].Title)
	}
}

func TestParseExportDataLegacyArray(t *testing.T) {
	tasks := []todo.Todo{todo.New("legacy task")}
	data, err := json.Marshal(tasks)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := parseExportData(data)
	if err != nil {
		t.Fatalf("parseExportData: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Legacy task" {
		t.Errorf("got %+v", got)
	}
}

func TestParseExportDataVersionTooHigh(t *testing.T) {
	env := exportEnvelope{Version: 999, ExportedAt: time.Now().UTC(), Tasks: nil}
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	_, err = parseExportData(data)
	if err == nil {
		t.Fatal("expected error for version > 1, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported export version") {
		t.Errorf("error = %q; want it to mention unsupported version", err.Error())
	}
}

func TestParseExportDataMalformedJSON(t *testing.T) {
	_, err := parseExportData([]byte(`{not valid json`))
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

func TestParseExportDataMalformedArray(t *testing.T) {
	_, err := parseExportData([]byte(`[{bad`))
	if err == nil {
		t.Fatal("expected error for malformed JSON array, got nil")
	}
}

func TestParseExportDataEmptyInput(t *testing.T) {
	_, err := parseExportData([]byte(""))
	if err == nil {
		t.Fatal("expected error for empty input, got nil")
	}
}

func TestParseExportDataUnknownFormat(t *testing.T) {
	_, err := parseExportData([]byte(`"just a string"`))
	if err == nil {
		t.Fatal("expected error for unrecognised format, got nil")
	}
}

// ── round-trip integration tests (against the shared test DB) ─────────────────

// TestExportImportRoundTrip exports tasks and imports them into a fresh
// in-memory DB, asserting the tasks survive the round trip intact.
func TestExportImportRoundTrip(t *testing.T) {
	h := openTestDB(t)

	// Seed two tasks with various fields.
	a := todo.New("round-trip-A")
	a.Priority = todo.PriorityHigh
	a.Tags = []string{"x", "y"}
	a.Notes = "some notes"
	a.AddComment("first comment")

	b := todo.New("round-trip-B")
	b.Project = "myproject"
	b.AddDependency(a.ID) // B depends on A

	saveTodos(t, h, []todo.Todo{a, b})

	// Load and build an export envelope.
	all, err := loadTodosFromDB(h)
	if err != nil {
		t.Fatalf("load for export: %v", err)
	}
	env := exportEnvelope{Version: 1, ExportedAt: time.Now().UTC(), Tasks: all}
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	// Import into a fresh DB.
	fresh := openTestDB(t)
	tasks, err := parseExportData(data)
	if err != nil {
		t.Fatalf("parseExportData: %v", err)
	}
	_, _, err = mergeIntoStore(fresh, tasks, rank.DefaultBiases())
	if err != nil {
		t.Fatalf("mergeIntoStore: %v", err)
	}

	// Verify the fresh DB has the same tasks.
	got, err := loadTodosFromDB(fresh)
	if err != nil {
		t.Fatalf("load fresh: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 tasks after round-trip, got %d", len(got))
	}
	byID := make(map[string]todo.Todo, len(got))
	for _, g := range got {
		byID[g.ID] = g
	}

	ga, ok := byID[a.ID]
	if !ok {
		t.Fatal("task A missing after round-trip")
	}
	if ga.Priority != todo.PriorityHigh {
		t.Errorf("A.Priority = %v, want high", ga.Priority)
	}
	if len(ga.Tags) != 2 {
		t.Errorf("A.Tags = %v, want [x y]", ga.Tags)
	}
	if ga.Notes != "some notes" {
		t.Errorf("A.Notes = %q, want %q", ga.Notes, "some notes")
	}
	if len(ga.Comments) != 1 {
		t.Errorf("A.Comments = %d, want 1", len(ga.Comments))
	}

	gb, ok := byID[b.ID]
	if !ok {
		t.Fatal("task B missing after round-trip")
	}
	if gb.Project != "myproject" {
		t.Errorf("B.Project = %q, want myproject", gb.Project)
	}
	if len(gb.Dependencies) != 1 || gb.Dependencies[0] != a.ID {
		t.Errorf("B.Dependencies = %v, want [%s]", gb.Dependencies, a.ID)
	}
}

// TestImportLegacyBareArray imports a legacy bare JSON array and confirms
// the tasks are merged into a fresh DB.
func TestImportLegacyBareArray(t *testing.T) {
	task := todo.New("legacy import target")
	data, err := json.Marshal([]todo.Todo{task})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	h := openTestDB(t)
	tasks, err := parseExportData(data)
	if err != nil {
		t.Fatalf("parseExportData: %v", err)
	}
	_, _, err = mergeIntoStore(h, tasks, rank.DefaultBiases())
	if err != nil {
		t.Fatalf("mergeIntoStore: %v", err)
	}

	got, err := loadTodosFromDB(h)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Legacy import target" {
		t.Errorf("got %+v", got)
	}
}

// TestImportIdempotent: importing the same file twice must leave the store
// unchanged on the second import (changed=false).
func TestImportIdempotent(t *testing.T) {
	task := todo.New("idempotent-check")
	env := exportEnvelope{Version: 1, ExportedAt: time.Now().UTC(), Tasks: []todo.Todo{task}}
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	h := openTestDB(t)
	tasks, err := parseExportData(data)
	if err != nil {
		t.Fatalf("parseExportData: %v", err)
	}

	// First import: must change the store.
	_, changed1, err := mergeIntoStore(h, tasks, rank.DefaultBiases())
	if err != nil {
		t.Fatalf("first mergeIntoStore: %v", err)
	}
	if !changed1 {
		t.Error("first import: expected changed=true, got false")
	}

	// Second import with the same data: must be a no-op.
	tasks2, err := parseExportData(data)
	if err != nil {
		t.Fatalf("parseExportData (second): %v", err)
	}
	_, changed2, err := mergeIntoStore(h, tasks2, rank.DefaultBiases())
	if err != nil {
		t.Fatalf("second mergeIntoStore: %v", err)
	}
	if changed2 {
		t.Error("second import: expected changed=false (idempotent), got true")
	}
}

// TestCliImportVersion1Envelope exercises the full cliImport code path using a
// temp file on disk. The shared test DB (same $HOME as all CLI tests) is used
// — we just verify exit code 0 and that the task title appears in a subsequent
// list.
func TestCliImportVersion1Envelope(t *testing.T) {
	task := todo.New("cli-import-envelope-check")
	env := exportEnvelope{Version: 1, ExportedAt: time.Now().UTC(), Tasks: []todo.Todo{task}}
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	f, err := os.CreateTemp(t.TempDir(), "export-*.json")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	f.Close()

	out := captureStdout(t, func() {
		if code := cliImport([]string{f.Name()}); code != 0 {
			t.Errorf("cliImport exit %d", code)
		}
	})
	// The task is new to the store, so the summary must count it as changed.
	if !strings.Contains(out, "imported 1 task(s), 1 changed") {
		t.Errorf("expected summary line %q, got %q", "imported 1 task(s), 1 changed", out)
	}

	// The task should now be findable.
	_, todos, err := loadForCLI()
	if err != nil {
		t.Fatalf("loadForCLI: %v", err)
	}
	_, err = findTaskByRef(todoPtrs(todos), "cli-import-envelope-check")
	if err != nil {
		t.Errorf("imported task not found: %v", err)
	}
}

// TestCliImportStdinLargeSingleLine guards the stdin reader against per-token
// buffer caps: a JSON export is one long line, so a scanner-based reader would
// fail a >4MB import with "token too long". The payload is one task with a
// ~5MB notes field, marshalled to a single line and fed through a real file
// swapped in as os.Stdin.
func TestCliImportStdinLargeSingleLine(t *testing.T) {
	task := todo.New("huge-stdin-import-check")
	task.Notes = strings.Repeat("x", 5*1024*1024)
	env := exportEnvelope{Version: 1, ExportedAt: time.Now().UTC(), Tasks: []todo.Todo{task}}
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(data) < 4*1024*1024 {
		t.Fatalf("payload too small to exercise the buffer cap: %d bytes", len(data))
	}

	path := filepath.Join(t.TempDir(), "huge.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open payload: %v", err)
	}
	defer f.Close()

	origStdin := os.Stdin
	os.Stdin = f
	defer func() { os.Stdin = origStdin }()

	out := captureStdout(t, func() {
		if code := cliImport([]string{"-"}); code != 0 {
			t.Errorf("cliImport - exit %d", code)
		}
	})
	if !strings.Contains(out, "imported 1 task(s), 1 changed") {
		t.Errorf("expected summary line, got %q", out)
	}

	// Clean up: tombstone the 5MB task so it doesn't leak into every later
	// test sharing this DB. Left live, a later cliExport under captureStdout
	// would deadlock — the capture pipe fills at ~64KB with no reader until
	// the captured fn returns, and the giant notes field blows well past it.
	tomb := task
	tomb.Deleted = true
	tomb.DeletedAt = time.Now()
	tomb.ModifiedAt = time.Now()
	if _, _, err := mergeIntoStore(testStore(t), []todo.Todo{tomb}, rank.DefaultBiases()); err != nil {
		t.Fatalf("cleanup tombstone: %v", err)
	}
}

// TestCliImportEditOnlyReportsChanged guards the summary's honesty: an import
// that only edits an existing task leaves the live-task count unchanged, but
// the merge did work — the changed count must be 1, not 0.
func TestCliImportEditOnlyReportsChanged(t *testing.T) {
	task := todo.New("edit-only-import-check")
	writeImport := func(tk todo.Todo) string {
		t.Helper()
		env := exportEnvelope{Version: 1, ExportedAt: time.Now().UTC(), Tasks: []todo.Todo{tk}}
		data, err := json.Marshal(env)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		path := filepath.Join(t.TempDir(), "edit.json")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		return path
	}

	// Seed the task.
	if code := cliImport([]string{writeImport(task)}); code != 0 {
		t.Fatalf("seed import: exit %d", code)
	}

	// Re-import the same task with a later edit: count unchanged, content not.
	edited := task
	edited.Title = "Edit-only import check (renamed)"
	edited.ModifiedAt = task.ModifiedAt.Add(time.Hour)
	out := captureStdout(t, func() {
		if code := cliImport([]string{writeImport(edited)}); code != 0 {
			t.Errorf("edit import: exit %d", code)
		}
	})
	if !strings.Contains(out, "imported 1 task(s), 1 changed") {
		t.Errorf("edit-only import must report 1 changed, got %q", out)
	}
}

// TestCliImportVersionTooHighExitsWithError confirms that an envelope with
// version > 1 causes exit code 1 (runtime error), not 0 or 2.
func TestCliImportVersionTooHighExitsWithError(t *testing.T) {
	env := exportEnvelope{Version: 999, ExportedAt: time.Now().UTC(), Tasks: nil}
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	f, err := os.CreateTemp(t.TempDir(), "future-*.json")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatalf("write: %v", err)
	}
	f.Close()

	code := cliImport([]string{f.Name()})
	if code != 1 {
		t.Errorf("want exit 1 for version > 1, got %d", code)
	}
}

// TestCliImportMalformedJSONExitsWithError confirms malformed input is exit 1.
func TestCliImportMalformedJSONExitsWithError(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "bad-*.json")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	if _, err := f.WriteString(`{this is not json`); err != nil {
		t.Fatalf("write: %v", err)
	}
	f.Close()

	code := cliImport([]string{f.Name()})
	if code != 1 {
		t.Errorf("want exit 1 for malformed JSON, got %d", code)
	}
}

// TestCliImportMissingArgExitsWithUsage confirms that no arguments produces
// exit 2 (usage error).
func TestCliImportMissingArgExitsWithUsage(t *testing.T) {
	code := cliImport([]string{})
	if code != 2 {
		t.Errorf("want exit 2 for missing arg, got %d", code)
	}
}

// TestCliImportNonExistentFileExitsWithError confirms a missing file is exit 1.
func TestCliImportNonExistentFileExitsWithError(t *testing.T) {
	code := cliImport([]string{filepath.Join(t.TempDir(), "no-such-file.json")})
	if code != 1 {
		t.Errorf("want exit 1 for missing file, got %d", code)
	}
}

// TestCliExportEmitsEnvelope verifies that `tjek export` now produces a
// versioned envelope rather than a bare JSON array.
func TestCliExportEmitsEnvelope(t *testing.T) {
	// Seed a task so the export is non-empty.
	if code := cliAdd([]string{"export-envelope-guard"}); code != 0 {
		t.Fatalf("add: exit %d", code)
	}

	out := captureStdout(t, func() {
		if code := cliExport([]string{}); code != 0 {
			t.Fatalf("export: exit %d", code)
		}
	})

	var env exportEnvelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("output is not a valid envelope: %v\nraw: %s", err, out)
	}
	if env.Version != 1 {
		t.Errorf("envelope.version = %d, want 1", env.Version)
	}
	if env.ExportedAt.IsZero() {
		t.Error("envelope.exported_at is zero")
	}
	if len(env.Tasks) == 0 {
		t.Error("envelope.tasks is empty, expected at least one task")
	}
}
