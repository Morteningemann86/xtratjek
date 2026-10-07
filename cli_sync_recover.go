package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Morteningemann86/xtratjek/paths"
	"github.com/Morteningemann86/xtratjek/todo"
)

// cli_sync_recover.go implements `tjek sync --recover` and `tjek sync
// --recover=<ref>`. The sync log (sync.log, state directory) is an append-only file
// of JSON lines written by logDroppedEdits. This file parses those lines,
// presents them human-readably, and reapplies one entry through the normal
// save path so the recovered edit propagates on the next regular sync.
//
// Consume tracking: when an entry is reapplied we append a recovery marker
// line (same JSON shape, note="recovered") to the log. On subsequent --recover
// runs the listing skips any task whose most-recent log entry is a recovery
// marker — so recovered entries don't nag forever while the log stays
// append-only. For tasks with multiple dropped-edit entries, --recover=<ref>
// applies the most recent unrecovered one (the last writer a user is most
// likely to care about), then marks it consumed.

// recoverAbsent is the sentinel that cliSync uses for the --recover string
// flag's zero value — it distinguishes "flag was not provided at all" from
// "flag was provided with an empty value (list mode)". An empty-string default
// would collide with the list-mode case.
const recoverAbsent = "\x00absent"

// normaliseBareRecover rewrites a bare "--recover" or "-recover" token in args
// to "--recover=" so stdlib's flag package treats it as a string flag with an
// empty value (our list-mode sentinel) rather than requiring the next arg as
// the flag's value.
func normaliseBareRecover(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if a == "--recover" || a == "-recover" {
			out[i] = "--recover="
		} else {
			out[i] = a
		}
	}
	return out
}

// syncLogNote values embedded in log lines.
const (
	syncLogNoteDropped   = "local edit superseded by sync (last-writer-wins)"
	syncLogNoteRecovered = "recovered"
)

// syncLogEntry mirrors the JSON written by logDroppedEdits and the recovery
// marker appended here.
type syncLogEntry struct {
	At      string    `json:"at"`
	Note    string    `json:"note"`
	Dropped todo.Todo `json:"dropped"`
}

// parseSyncLog reads all entries from the log file at path. Missing file
// returns (nil, nil) — an empty log is not an error.
func parseSyncLog(path string) ([]syncLogEntry, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []syncLogEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20) // 1 MiB per line — tasks can be large
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e syncLogEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			// Skip malformed lines rather than aborting the whole listing.
			continue
		}
		entries = append(entries, e)
	}
	return entries, sc.Err()
}

// activeDroppedEdits returns, for each task ID, the most-recent dropped-edit
// entry that has not been marked consumed by a later recovery marker. The
// returned map is keyed by task ID; the order slice preserves first-appearance
// order for stable listing.
func activeDroppedEdits(entries []syncLogEntry) (byID map[string]syncLogEntry, order []string) {
	byID = make(map[string]syncLogEntry)
	seen := make(map[string]bool)
	for _, e := range entries {
		id := e.Dropped.ID
		if id == "" {
			continue
		}
		switch e.Note {
		case syncLogNoteDropped:
			if !seen[id] {
				order = append(order, id)
				seen[id] = true
			}
			byID[id] = e // later dropped entry supersedes earlier one
		case syncLogNoteRecovered:
			delete(byID, id) // consumed — hide from listing
		}
	}
	// Keep order slice only for IDs that still have active entries.
	filtered := order[:0]
	for _, id := range order {
		if _, ok := byID[id]; ok {
			filtered = append(filtered, id)
		}
	}
	order = filtered
	return byID, order
}

// printDroppedEdits lists the active (not yet recovered) dropped-edit entries
// in a human-readable format. Returns a process exit code.
func printDroppedEdits(logPath string) int {
	entries, err := parseSyncLog(logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tjek sync --recover: read log: %v\n", err)
		return 1
	}
	byID, order := activeDroppedEdits(entries)
	if len(order) == 0 {
		fmt.Println("(no dropped edits in sync log)")
		return 0
	}
	fmt.Printf("%-8s  %-19s  %s\n", "ID", "DROPPED AT", "TITLE")
	for _, id := range order {
		e := byID[id]
		t := e.Dropped
		when := e.At
		if pt, perr := time.Parse(time.RFC3339Nano, when); perr == nil {
			when = pt.Local().Format("2006-01-02 15:04:05")
		}
		shortID := id
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		fmt.Printf("%-8s  %-19s  %s\n", shortID, when, t.Title)

		// Show which scalar fields differ from an empty baseline — give the
		// user enough context to decide whether to recover.
		if t.Priority != todo.PriorityMedium {
			fmt.Printf("          priority: %s\n", t.Priority.String())
		}
		if t.Size != todo.SizeMedium {
			fmt.Printf("          size: %s\n", t.Size.String())
		}
		if t.Project != "" {
			fmt.Printf("          project: %s\n", t.Project)
		}
		if len(t.Tags) > 0 {
			fmt.Printf("          tags: %s\n", strings.Join(t.Tags, ", "))
		}
		if !t.DueDate.IsZero() {
			fmt.Printf("          due: %s\n", t.DueDate.Format("2006-01-02"))
		}
		if t.Notes != "" {
			preview := t.Notes
			if len(preview) > 80 {
				preview = preview[:77] + "..."
			}
			fmt.Printf("          notes: %s\n", preview)
		}
		if t.Status == todo.Done {
			fmt.Printf("          status: done\n")
		}
	}
	return 0
}

// reapplyDroppedEdit looks up the most-recent active dropped-edit entry for
// ref, applies its scalar fields through the normal save path (so
// StampModified's clock-skew clamp applies), and writes a recovery marker to
// the log. Returns a process exit code.
func reapplyDroppedEdit(logPath, ref string) int {
	entries, err := parseSyncLog(logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tjek sync --recover: read log: %v\n", err)
		return 1
	}
	byID, order := activeDroppedEdits(entries)
	if len(order) == 0 {
		fmt.Fprintln(os.Stderr, "tjek sync --recover: no dropped edits in sync log")
		return 1
	}

	// Build the task ID list from active entries so findTaskByRef can resolve
	// the user's ref (id-prefix or title substring) against the logged tasks.
	loggedTasks := make([]todo.Todo, 0, len(order))
	for _, id := range order {
		loggedTasks = append(loggedTasks, byID[id].Dropped)
	}

	loggedTask, err := findTaskByRef(todoPtrs(loggedTasks), ref)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tjek sync --recover: %v\n", err)
		return 2
	}

	// Load the live store to check the task still exists.
	repo, liveTodos, loadErr := loadForCLI()
	if loadErr != nil {
		fmt.Fprintf(os.Stderr, "tjek sync --recover: load store: %v\n", loadErr)
		return 1
	}

	live, findErr := findTaskByRef(todoPtrs(liveTodos), loggedTask.ID)
	if findErr != nil {
		// The task is gone from the live store (deleted elsewhere). Don't resurrect it.
		fmt.Fprintf(os.Stderr, "tjek sync --recover: task %s no longer exists locally (it may have been deleted), so it cannot be reapplied\n",
			loggedTask.ID[:8])
		return 1
	}

	// Apply the logged scalar fields onto the live task. Use the set-methods
	// that call StampModified internally, just like `tjek edit` does, so the
	// monotonic clock-skew clamp is guaranteed. We apply each field
	// unconditionally from the log entry (the user asked to restore exactly
	// that state). Child collections (comments, time entries) and
	// tags/deps merge independently in the sync engine — we only touch the
	// scalar fields that DroppedLocalEdits compares in scalarHash.
	logged := loggedTask

	// Title: set directly then stamp (mirrors cliEdit's title path).
	live.Title = todo.CapitalizeTitle(logged.Title)
	live.ModifiedAt = todo.StampModified(live.ModifiedAt)

	// Status: use Toggle only if it needs to change to avoid double-toggling.
	if live.Status != logged.Status {
		live.Toggle() // Toggle calls StampModified internally
	}

	// Priority, Size, Project, Due, Start, Notes: use the setter methods.
	if live.Priority != logged.Priority {
		live.SetPriority(logged.Priority)
	}
	if live.Size != logged.Size {
		live.SetSize(logged.Size)
	}
	if live.Project != logged.Project {
		live.SetProject(logged.Project)
	}
	if !live.DueDate.Equal(logged.DueDate) {
		if logged.DueDate.IsZero() {
			live.DueDate = time.Time{}
			live.ModifiedAt = todo.StampModified(live.ModifiedAt)
		} else {
			live.SetDueDate(logged.DueDate)
		}
	}
	if !live.StartDate.Equal(logged.StartDate) {
		if logged.StartDate.IsZero() {
			live.StartDate = time.Time{}
			live.ModifiedAt = todo.StampModified(live.ModifiedAt)
		} else {
			// SetStartDate normalises "today" to wall-clock — use it directly.
			live.StartDate = logged.StartDate
			live.ModifiedAt = todo.StampModified(live.ModifiedAt)
		}
	}
	if live.Notes != logged.Notes {
		live.SetNotes(logged.Notes)
	}
	if live.Recurrence != logged.Recurrence {
		if logged.Recurrence == "" {
			live.ClearRecurrence()
		} else {
			live.SetRecurrence(logged.Recurrence)
		}
	}

	if err := repo.Save([]*todo.Todo{live}, nil); err != nil {
		fmt.Fprintf(os.Stderr, "tjek sync --recover: save: %v\n", err)
		return 1
	}

	// Append the recovery marker to the log so --recover no longer shows this entry.
	if markerErr := appendRecoveryMarker(logPath, byID[loggedTask.ID]); markerErr != nil {
		fmt.Fprintf(os.Stderr, "tjek sync --recover: warning: could not write recovery marker: %v\n", markerErr)
		// Don't fail — the task was saved successfully.
	}

	fmt.Printf("recovered  %s  %s\n", live.ID[:8], live.Title)
	fmt.Fprintln(os.Stderr, "(changes will propagate on the next tjek sync)")
	return 0
}

// appendRecoveryMarker appends a "recovered" marker line to the log at path
// so subsequent --recover listings skip this entry.
func appendRecoveryMarker(path string, original syncLogEntry) error {
	if _, err := paths.Ensure(paths.State); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	marker := syncLogEntry{
		At:      time.Now().UTC().Format(time.RFC3339Nano),
		Note:    syncLogNoteRecovered,
		Dropped: original.Dropped,
	}
	line, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}
