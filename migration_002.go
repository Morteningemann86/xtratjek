package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Morteningemann86/xtratjek/paths"
	"github.com/Morteningemann86/xtratjek/rank"
	"github.com/Morteningemann86/xtratjek/todo"
)

func init() {
	goMigrations[2] = backfillNormalizedTables
}

// backfillNormalizedTables reads every row's JSON blob from the legacy `data`
// column, parses it, and inserts the nested data (tags, dependencies, comments,
// learnings, time entries) into the new child tables. It also fills in the new
// `notes`, `completed_at`, and `urgency` columns from the parsed struct
// (the column is later renamed to `sequence` by migration 004; references
// inside this migration use the historical name that existed at its
// snapshot in time).
//
// Before any destructive work the function writes a one-shot JSON backup next
// to the DB so the user can recover if the migration is later found to have
// dropped data. The backup is named with the current timestamp.
func backfillNormalizedTables(tx *sql.Tx) error {
	// Best-effort backup. A failure to write the backup is non-fatal — the
	// migration still proceeds because the underlying `data` blobs remain in
	// the DB row (we don't drop the column in this migration).
	if path, err := writePreNormalizeBackup(tx); err == nil && path != "" {
		fmt.Fprintf(os.Stderr, "tjek: wrote pre-normalize backup to %s\n", path)
	}

	rows, err := tx.Query(`SELECT id, data FROM todos`)
	if err != nil {
		return fmt.Errorf("scan todos: %w", err)
	}
	defer rows.Close()

	insertTag, err := tx.Prepare(`INSERT OR IGNORE INTO task_tags (task_id, tag) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer insertTag.Close()
	insertDep, err := tx.Prepare(`INSERT OR IGNORE INTO task_dependencies (task_id, depends_on_id) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer insertDep.Close()
	insertComment, err := tx.Prepare(`INSERT OR REPLACE INTO task_comments (id, task_id, text, created_at) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer insertComment.Close()
	insertLearning, err := tx.Prepare(`INSERT OR REPLACE INTO task_learnings (id, task_id, text, created_at) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer insertLearning.Close()
	insertEntry, err := tx.Prepare(`INSERT OR REPLACE INTO task_time_entries (id, task_id, started_at, stopped_at) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer insertEntry.Close()
	updateScalars, err := tx.Prepare(`UPDATE todos SET notes=?, completed_at=?, urgency=? WHERE id=?`)
	if err != nil {
		return err
	}
	defer updateScalars.Close()

	type row struct{ id, data string }
	var batch []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.data); err != nil {
			return err
		}
		batch = append(batch, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()

	for _, r := range batch {
		var t todo.Todo
		if err := json.Unmarshal([]byte(r.data), &t); err != nil {
			return fmt.Errorf("parse blob for %s: %w", r.id, err)
		}
		// Learnings left the domain type in migration 011, but a blob written
		// before then still carries them, and 011 folds whatever lands in
		// task_learnings into the task's notes — so decoding them separately
		// keeps an ancient backup lossless through the whole chain.
		var legacy struct {
			Learnings []struct {
				ID        string    `json:"id"`
				Text      string    `json:"text"`
				CreatedAt time.Time `json:"created_at"`
			} `json:"learnings"`
		}
		if err := json.Unmarshal([]byte(r.data), &legacy); err != nil {
			return fmt.Errorf("parse blob learnings for %s: %w", r.id, err)
		}
		for _, tg := range t.Tags {
			if _, err := insertTag.Exec(r.id, tg); err != nil {
				return err
			}
		}
		for _, dep := range t.Dependencies {
			if _, err := insertDep.Exec(r.id, dep); err != nil {
				return err
			}
		}
		for _, c := range t.Comments {
			if _, err := insertComment.Exec(c.ID, r.id, c.Text, fmtTime(c.CreatedAt)); err != nil {
				return err
			}
		}
		for _, l := range legacy.Learnings {
			if _, err := insertLearning.Exec(l.ID, r.id, l.Text, fmtTime(l.CreatedAt)); err != nil {
				return err
			}
		}
		for _, e := range t.TimeEntries {
			if _, err := insertEntry.Exec(e.ID, r.id, fmtTime(e.StartedAt), fmtTime(e.StoppedAt)); err != nil {
				return err
			}
		}
		if _, err := updateScalars.Exec(t.Notes, fmtTime(t.CompletedAt), rank.Default().Score(&t), r.id); err != nil {
			return err
		}
	}
	return nil
}

// writePreNormalizeBackup dumps every live row's data blob to a JSON file
// alongside the DB so a user can recover if a future migration drops the data
// column. Returns the path written, or "" if no backup was needed/possible.
func writePreNormalizeBackup(tx *sql.Tx) (string, error) {
	dir, err := paths.Dir(paths.Data)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(dir); err != nil {
		return "", err
	}
	rows, err := tx.Query(`SELECT data FROM todos WHERE deleted = 0`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var blobs []json.RawMessage
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return "", err
		}
		blobs = append(blobs, json.RawMessage(s))
	}
	if len(blobs) == 0 {
		return "", nil
	}
	path := filepath.Join(dir, fmt.Sprintf("tasks-pre-normalize-%s.json.bak", time.Now().UTC().Format("20060102-150405")))
	body, err := json.MarshalIndent(blobs, "", "  ")
	if err != nil {
		return "", err
	}
	if err := writeFileAtomic(path, body, 0644); err != nil {
		return "", err
	}
	return path, nil
}
