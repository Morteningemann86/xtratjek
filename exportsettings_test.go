package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Morteningemann86/xtratjek/todo"
)

// Tab at a path prompt completes like a shell: as far as the names in the
// folder agree, with the separator once a folder is named, letter case taken
// from disk, and folders only where a folder is asked for.
func TestCompletePath(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"Documents", "Downloads", "Music"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "Music.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	sep := string(filepath.Separator)
	at := func(s string) string { return dir + sep + s }
	cases := []struct {
		typed    string
		dirsOnly bool
		want     string
	}{
		{at("Doc"), true, at("Documents" + sep)},
		{at("doc"), true, at("Documents" + sep)}, // case from disk
		{at("D"), true, at("Do")},                // Documents and Downloads agree on "Do"
		{at("Mus"), true, at("Music" + sep)},     // the file is not a folder
		{at("Mus"), false, at("Music")},          // Music/ and Music.json agree on "Music"
		{at("Music."), false, at("Music.json")},
		{at("Nothing"), true, at("Nothing")},
		{"~", true, "~"}, // a bare ~ is left alone, not sliced
	}
	for _, c := range cases {
		if got := completePath(c.typed, c.dirsOnly); got != c.want {
			t.Errorf("completePath(%q, %v) = %q, want %q", c.typed, c.dirsOnly, got, c.want)
		}
	}
	home, _ := os.UserHomeDir()
	if err := os.MkdirAll(filepath.Join(home, "Exports"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := completePath("~"+sep+"Expo", true); got != "~"+sep+"Exports"+sep {
		t.Errorf("a ~ path completes to %q, keeping the ~", got)
	}
}

// Setting a folder in Settings starts the export: tjek-export.json appears
// there holding every live task, done ones included, in the format
// `tjek import` reads. Later changes are paced into one write, quitting
// writes what is still due, and a blank folder turns it off.
func TestScriptAutoExport(t *testing.T) {
	open := todo.New("Open task")
	done := todo.New("Finished task")
	done.Status, done.CompletedAt = todo.Done, time.Now()
	m := modelWithTasks(t, open, done)
	m.tab = tabSettings
	folder := t.TempDir()
	file := filepath.Join(folder, exportFileName)

	m.settingsCursor = settingExportFolder
	m = sendKey(t, m, "enter")
	if m.mode != modeEditExportFolder {
		t.Fatalf("enter on the export row left mode %v", m.mode)
	}
	m.textInput.SetValue(folder)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.mode != modeNormal || m.exportFolder != folder || !m.exportScheduled {
		t.Fatalf("mode %v folder %q scheduled %v, want the folder saved and an export scheduled", m.mode, m.exportFolder, m.exportScheduled)
	}
	if s, _ := loadSettings(); s.ExportFolder != folder {
		t.Errorf("settings.json export_folder = %q", s.ExportFolder)
	}

	writeDue := func() {
		t.Helper()
		cmd := m.exportTick()
		if cmd == nil {
			t.Fatal("no export was due")
		}
		if msg := cmd().(exportDoneMsg); msg.err != nil {
			t.Fatal(msg.err)
		}
	}
	writeDue()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := parseExportData(data)
	if err != nil || len(tasks) != 2 {
		t.Fatalf("export holds %d task(s), err %v; want both, done included", len(tasks), err)
	}

	// Two changes inside the interval: one write, scheduled once.
	if cmd := m.exportSoon(); cmd == nil {
		t.Error("the first change after an export scheduled nothing")
	}
	if cmd := m.exportSoon(); cmd != nil {
		t.Error("a second change scheduled a second write")
	}
	m.add(todo.New("Added before quitting"))
	m = sendKey(t, m, "q")
	data, _ = os.ReadFile(file)
	if tasks, _ := parseExportData(data); len(tasks) != 3 {
		t.Errorf("quitting left %d task(s) in the export, want 3", len(tasks))
	}

	m.mode = modeNormal
	m = sendKey(t, m, "enter")
	m.textInput.SetValue("")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.exportFolder != "" || m.exportSoon() != nil {
		t.Error("a blank folder did not turn the export off")
	}
}

func TestScriptExportFolderMustExist(t *testing.T) {
	m := modelWithTasks(t)
	m.tab = tabSettings
	m.settingsCursor = settingExportFolder
	m = sendKey(t, m, "enter")
	m.textInput.SetValue(filepath.Join(t.TempDir(), "missing"))
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.mode != modeEditExportFolder || m.exportFolder != "" || !strings.Contains(m.err, "Not a folder") {
		t.Errorf("mode %v folder %q toast %q; want the prompt kept open with the reason", m.mode, m.exportFolder, m.err)
	}
}

// Import from file merges an export through the same merge as `tjek import`:
// a newer version of a task updates it, an unknown task is added, and u takes
// the whole import back in one step.
func TestScriptImportFromFile(t *testing.T) {
	setTestHome(t, t.TempDir())
	testStore(t)
	if code := cliAdd([]string{"Kept task"}); code != 0 {
		t.Fatalf("add: exit %d", code)
	}
	m := initialModel(newSQLiteRepo())
	m.termWidth, m.termHeight = 120, 40
	m.tab = tabSettings
	var kept todo.Todo
	for _, t := range m.allTodos() {
		kept = *t
	}

	edited := copyTodo(kept)
	edited.Title = "Kept task, edited elsewhere"
	edited.ModifiedAt = kept.ModifiedAt.Add(time.Hour)
	added := todo.New("Brand new task")
	file := filepath.Join(t.TempDir(), "backup.json")
	if err := writeExport(file, []todo.Todo{edited, added}); err != nil {
		t.Fatal(err)
	}

	m.settingsCursor = settingImportFile
	m = sendKey(t, m, "enter")
	if m.mode != modeImportFile {
		t.Fatalf("enter on the import row left mode %v", m.mode)
	}
	m.textInput.SetValue(file)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if cmd == nil || m.mode != modeNormal {
		t.Fatalf("a good file should start the import (mode %v)", m.mode)
	}
	done := cmd().(importDoneMsg)
	if done.err != nil || done.res.added != 1 || done.res.updated != 1 {
		t.Fatalf("import result %+v, err %v; want 1 new and 1 updated", done.res, done.err)
	}
	next, _ = m.Update(done)
	m = next.(model)
	if !strings.Contains(m.err, "1 new, 1 updated") {
		t.Errorf("toast = %q", m.err)
	}
	next, _ = m.Update(reloadedMsg{todos: done.todos})
	m = next.(model)
	titles := map[string]bool{}
	for _, t := range m.allTodos() {
		titles[t.Title] = true
	}
	if !titles["Kept task, edited elsewhere"] || !titles["Brand new task"] {
		t.Fatalf("after import the store holds %v", titles)
	}

	m.tab = tabTasks
	m = sendKey(t, m, "u")
	titles = map[string]bool{}
	for _, t := range m.allTodos() {
		titles[t.Title] = true
	}
	if len(titles) != 1 || !titles["Kept task"] {
		t.Errorf("u after the import left %v, want the one task as it was", titles)
	}
}

func TestScriptImportRejectsANonExport(t *testing.T) {
	m := modelWithTasks(t)
	m.tab = tabSettings
	file := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(file, []byte("just some notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.settingsCursor = settingImportFile
	m = sendKey(t, m, "enter")
	m.textInput.SetValue(file)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.mode != modeImportFile || !strings.Contains(m.err, "Import failed") {
		t.Errorf("mode %v toast %q; want the prompt kept open with the reason", m.mode, m.err)
	}
	if n := len(m.undoStack); n > 0 && m.undoStack[n-1].desc == "import" {
		t.Error("a file that is not an export started an import")
	}
}
