package main

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Morteningemann86/xtratjek/todo"
)

// Settings → Export: a folder tjek keeps an up-to-date export in
// (exportFileName, the document `tjek export --include-done` prints), and an
// import from a file through the same merge as `tjek import`.
//
// The export follows the data, not the keys: it is scheduled after the TUI
// saves a change or reloads one another process made, soon after the first
// and then at most once per exportInterval, so a burst of edits is one write —
// the folder is typically synced (OneDrive and the like), and every write is
// an upload. It is written atomically, so a reader never sees half a file,
// and once more on quit.

const (
	exportInterval = time.Minute
	exportDebounce = 2 * time.Second
)

type exportTickMsg struct{}

type exportDoneMsg struct {
	path string
	err  error
}

// importDoneMsg carries an import's result and the store as it stands after
// the merge, which the TUI adopts as it would any external change.
type importDoneMsg struct {
	res   importResult
	todos []todo.Todo
	err   error
}

// exportSoon notes that the export is out of date and schedules its write.
func (m *model) exportSoon() tea.Cmd {
	if m.exportFolder == "" {
		return nil
	}
	m.exportDirty = true
	if m.exportScheduled {
		return nil
	}
	m.exportScheduled = true
	delay := max(exportDebounce, time.Until(m.lastExport.Add(exportInterval)))
	return tea.Tick(delay, func(time.Time) tea.Msg { return exportTickMsg{} })
}

// exportTick writes the export if it is still due. The tasks are copied on
// the loop; encoding and writing happen on a command.
func (m *model) exportTick() tea.Cmd {
	m.exportScheduled = false
	if !m.exportDirty || m.exportFolder == "" {
		return nil
	}
	m.exportDirty = false
	m.lastExport = time.Now()
	path := filepath.Join(m.exportFolder, exportFileName)
	tasks := m.exportTasks()
	return func() tea.Msg { return exportDoneMsg{path: path, err: writeExport(path, tasks)} }
}

// flushExport writes a due export before the app exits, which cannot wait for
// a tick.
func (m *model) flushExport() {
	if m.exportFolder == "" || (!m.exportDirty && !m.exportScheduled) {
		return
	}
	path := filepath.Join(m.exportFolder, exportFileName)
	if err := writeExport(path, m.exportTasks()); err != nil {
		fmt.Fprintf(os.Stderr, "Auto-export on quit failed: %v\n", err)
	}
	m.exportDirty, m.exportScheduled = false, false
}

// exportTasks is every live task, done ones included, copied so the write can
// run beside further edits, and ordered by ID so an unchanged task keeps its
// place in the file and a synced folder uploads a small diff.
func (m *model) exportTasks() []todo.Todo {
	out := make([]todo.Todo, 0, m.Store.len())
	for _, t := range m.allTodos() {
		out = append(out, copyTodo(*t))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func writeExport(path string, tasks []todo.Todo) error {
	data, err := exportJSON(tasks, time.Now())
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data, 0o644)
}

// ── The Settings rows ────────────────────────────────────────────────────────

func (m model) openExportFolderEditor() (tea.Model, tea.Cmd) {
	m.mode = modeEditExportFolder
	m.textInput.SetValue(m.exportFolder)
	m.textInput.CursorEnd()
	m.textInput.Placeholder = tr("Folder to keep tjek-export.json in (blank turns it off)")
	m.textInput.Focus()
	return m, textinput.Blink
}

func (m model) openImportPrompt() (tea.Model, tea.Cmd) {
	m.mode = modeImportFile
	start := ""
	if m.exportFolder != "" {
		start = m.exportFolder + string(filepath.Separator)
	}
	m.textInput.SetValue(start)
	m.textInput.CursorEnd()
	m.textInput.Placeholder = tr("Path to a tjek export (.json)")
	m.textInput.Focus()
	return m, textinput.Blink
}

// updateEditExportFolder edits the auto-export folder: tab completes a folder
// name, enter saves (blank switches the export off), esc leaves it as it was.
func (m model) updateEditExportFolder(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "tab":
			m.textInput.SetValue(completePath(m.textInput.Value(), true))
			m.textInput.CursorEnd()
			return m, nil
		case "enter":
			typed := strings.TrimSpace(m.textInput.Value())
			if typed == "" {
				m.exportFolder = ""
				m.mode = modeNormal
				m.persistSettings()
				m.flashInfo(tr("Auto-export off"))
				return m, clearErrAfter()
			}
			folder, err := filepath.Abs(expandHome(typed))
			if info, statErr := os.Stat(folder); err != nil || statErr != nil || !info.IsDir() {
				m.flashError(fmt.Sprintf(tr("Not a folder: %s"), typed))
				return m, clearErrAfter()
			}
			m.exportFolder = folder
			m.mode = modeNormal
			m.persistSettings()
			m.lastExport = time.Time{} // the first export goes out now, not in a minute
			m.flashInfo(fmt.Sprintf(tr("Exporting to %s"), filepath.Join(folder, exportFileName)))
			return m, tea.Batch(clearErrAfter(), m.exportSoon())
		case "esc":
			m.mode = modeNormal
			return m, nil
		}
	}
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

// updateImportFile takes the path of an export to merge in. A file that cannot
// be read or is not an export keeps the prompt open with the reason; a good
// one is merged on a command, as one undo step.
func (m model) updateImportFile(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "tab":
			m.textInput.SetValue(completePath(m.textInput.Value(), false))
			m.textInput.CursorEnd()
			return m, nil
		case "enter":
			path := expandHome(strings.TrimSpace(m.textInput.Value()))
			data, err := os.ReadFile(path)
			if err == nil {
				var tasks []todo.Todo
				if tasks, err = parseExportData(data); err == nil {
					m.mode = modeNormal
					return m, m.startImport(tasks)
				}
			}
			m.flashError(fmt.Sprintf(tr("Import failed: %v"), err))
			return m, clearErrAfter()
		case "esc":
			m.mode = modeNormal
			return m, nil
		}
	}
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

// startImport merges tasks into the store. The undo entry names every task in
// the file, so u restores the ones it changed and removes the ones it added.
// Edits still inside the save debounce are written first, since the merge
// reads the store from disk.
func (m *model) startImport(tasks []todo.Todo) tea.Cmd {
	ids := make([]string, 0, len(tasks))
	for i := range tasks {
		ids = append(ids, tasks[i].ID)
	}
	m.pushUndo("import", ids...)
	dirty, tombstones := m.Store.drainDirty()
	m.savePending = false
	repo, biases := m.repo, m.rank.Biases
	if m.watcher != nil {
		m.watcher.recordSelfSave()
	}
	return func() tea.Msg {
		if len(dirty) > 0 || len(tombstones) > 0 {
			if err := repo.Save(dirty, tombstones); err != nil {
				return importDoneMsg{err: err}
			}
		}
		res, err := importTasks(db, tasks, biases)
		if err != nil {
			return importDoneMsg{err: err}
		}
		todos, err := repo.Load()
		return importDoneMsg{res: res, todos: todos, err: err}
	}
}

// handleImportDone reports an import and adopts the merged store.
func (m model) handleImportDone(msg importDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.flashError(fmt.Sprintf(tr("Import failed: %v"), msg.err))
		return m, clearErrAfter()
	}
	if msg.res.added+msg.res.updated == 0 {
		// Nothing to undo, so no undo step for it either.
		if n := len(m.undoStack); n > 0 && m.undoStack[n-1].desc == "import" {
			m.undoStack = m.undoStack[:n-1]
		}
		m.flashInfo(tr("Nothing to import: every task in the file is already here"))
		return m, clearErrAfter()
	}
	m.flashSuccess(fmt.Sprintf(tr("Imported %d new, %d updated · u undoes it"), msg.res.added, msg.res.updated))
	todos := msg.todos
	return m, tea.Batch(clearErrAfter(), func() tea.Msg { return reloadedMsg{todos: todos} })
}

// exportFolderDisplay is the Settings row's value: the folder, with the home
// directory shortened to ~, or Off.
func exportFolderDisplay(folder string) string {
	if folder == "" {
		return tr("Off")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(folder, home) {
		return "~" + folder[len(home):]
	}
	return folder
}

// ── Paths typed at a prompt ──────────────────────────────────────────────────

// expandHome reads a leading ~ as the home directory. These are paths the user
// types for their own files, not tjek's (which go through paths.For).
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return home + p[1:]
		}
	}
	return p
}

// completePath extends the last element of a typed path as far as the names
// in its folder agree, the way a shell's tab does, and adds the separator
// once it names a folder; dirsOnly leaves files out. Letter case is matched
// loosely and taken from the name on disk. A path with nothing to add comes
// back as typed.
func completePath(typed string, dirsOnly bool) string {
	dir, prefix := filepath.Split(expandHome(typed))
	// The prefix is the typed text's own last element — unless the text is a
	// bare ~, whose expansion's last element was never typed.
	if !strings.HasSuffix(typed, prefix) {
		return typed
	}
	entries, err := os.ReadDir(cmp.Or(dir, "."))
	if err != nil {
		return typed
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		isDir := e.IsDir()
		if !isDir && e.Type()&os.ModeSymlink != 0 {
			if info, err := os.Stat(filepath.Join(dir, name)); err == nil {
				isDir = info.IsDir()
			}
		}
		if (dirsOnly && !isDir) || !strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix)) {
			continue
		}
		if isDir {
			name += string(filepath.Separator)
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return typed
	}
	common := names[0]
	for _, n := range names[1:] {
		common = commonPrefixFold(common, n)
	}
	if len(common) < len(prefix) || (len(common) == len(prefix) && common == prefix) {
		return typed
	}
	return typed[:len(typed)-len(prefix)] + common
}

// commonPrefixFold is the longest prefix a and b share ignoring letter case,
// spelled as in a.
func commonPrefixFold(a, b string) string {
	ra, rb := []rune(a), []rune(b)
	n := 0
	for n < len(ra) && n < len(rb) && strings.EqualFold(string(ra[n]), string(rb[n])) {
		n++
	}
	return string(ra[:n])
}
