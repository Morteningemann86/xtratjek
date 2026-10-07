package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Morteningemann86/xtratjek/rank"
	"github.com/Morteningemann86/xtratjek/todo"
)

// exportEnvelope is the versioned wrapper emitted by `tjek export`.
// Version 1 is the only defined version; readers must reject any version > 1
// with a clear error so future format changes do not silently corrupt data.
type exportEnvelope struct {
	Version    int         `json:"version"`
	ExportedAt time.Time   `json:"exported_at"`
	Tasks      []todo.Todo `json:"tasks"`
}

// ── import ────────────────────────────────────────────────────────────────────

func cliImport(args []string) int {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `usage: tjek import <file>
       tjek import -              read from stdin

Merges the tasks in the export file into the local store. Both the versioned
envelope produced by 'tjek export' and the legacy bare JSON array are accepted.
Import is idempotent: running it a second time with the same file changes nothing.`)
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}

	src := fs.Arg(0)
	var data []byte
	var err error
	if src == "-" {
		// io.ReadAll, not a line scanner: a JSON export is one long line, and
		// any per-token buffer cap would fail large imports with "token too long".
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(filepath.Clean(src))
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "tjek import: read: %v\n", err)
		return 1
	}

	tasks, err := parseExportData(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tjek import: %v\n", err)
		return 1
	}

	// Get the DB handle through the same path that sync uses: openStore sets
	// up the package-level `db` singleton and applies all migrations.
	if err := openStore(); err != nil {
		fmt.Fprintf(os.Stderr, "tjek import: open store: %v\n", err)
		return 1
	}
	res, err := importTasks(db, tasks, storedBiases())
	if err != nil {
		fmt.Fprintf(os.Stderr, "tjek import: %v\n", err)
		return 1
	}
	fmt.Printf("imported %d task(s), %d changed\n", len(tasks), res.added+res.updated)
	return 0
}

// importResult counts what an import did to the store: tasks it did not have,
// and tasks it had in an older version.
type importResult struct{ added, updated int }

// importTasks merges an export's tasks into the store — the sync merge, so it
// never replaces anything wholesale and a second import of the same file
// changes nothing. The CLI's `tjek import` and the Settings import share it,
// so the two cannot disagree about what a file does.
func importTasks(h *sql.DB, tasks []todo.Todo, b rank.Biases) (importResult, error) {
	// Snapshot the pre-merge set so the result reports what the merge actually
	// changed (new or edited), not a live-count delta: an import that only
	// edits existing tasks leaves the count unchanged but still did work.
	before, err := loadTodosForSync(h)
	if err != nil {
		return importResult{}, fmt.Errorf("load current: %w", err)
	}
	merged, changed, err := mergeIntoStore(h, tasks, b)
	if err != nil {
		return importResult{}, fmt.Errorf("merge: %w", err)
	}
	var res importResult
	if !changed {
		return res, nil
	}
	known := make(map[string]bool, len(before))
	for i := range before {
		known[before[i].ID] = true
	}
	for _, t := range changedTasks(before, merged) {
		if known[t.ID] {
			res.updated++
		} else {
			res.added++
		}
	}
	return res, nil
}

// exportFileName is the file the Settings auto-export keeps current in the
// folder it is given.
const exportFileName = "tjek-export.json"

// exportJSON is the export document for tasks as `tjek export` writes it:
// the versioned envelope, indented, with a trailing newline.
func exportJSON(tasks []todo.Todo, at time.Time) ([]byte, error) {
	data, err := json.MarshalIndent(exportEnvelope{Version: 1, ExportedAt: at.UTC(), Tasks: tasks}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// parseExportData sniffs the leading non-whitespace byte to distinguish a
// bare JSON array (legacy) from the versioned envelope object.
func parseExportData(data []byte) ([]todo.Todo, error) {
	// Skip leading whitespace to find the first structural byte.
	first := byte(0)
	for _, b := range data {
		if b != ' ' && b != '\t' && b != '\r' && b != '\n' {
			first = b
			break
		}
	}
	switch first {
	case '[':
		// Legacy bare array.
		var tasks []todo.Todo
		if err := json.Unmarshal(data, &tasks); err != nil {
			return nil, fmt.Errorf("malformed JSON array: %w", err)
		}
		return tasks, nil
	case '{':
		// Versioned envelope.
		var env exportEnvelope
		if err := json.Unmarshal(data, &env); err != nil {
			return nil, fmt.Errorf("malformed export envelope: %w", err)
		}
		if env.Version > 1 {
			return nil, fmt.Errorf("unsupported export version %d (this build only knows version 1; upgrade tjek to import this file)", env.Version)
		}
		return env.Tasks, nil
	case 0:
		return nil, fmt.Errorf("empty input")
	default:
		return nil, fmt.Errorf("unrecognised format (expected JSON object or array, got %q)", string(first))
	}
}
