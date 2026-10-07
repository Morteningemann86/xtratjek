package main

import (
	"database/sql"
	"fmt"

	"github.com/Morteningemann86/xtratjek/todo"
)

func init() {
	goMigrations[11] = normalizeStoredTags
}

// normalizeStoredTags converts legacy tags containing whitespace/mixed case
// to the same slug form Todo.AddTag now enforces. Replacing each affected
// task's set wholesale lets aliases such as "Deep Work" and "deep-work"
// collapse without violating task_tags' (task_id, tag) primary key. ModifiedAt
// is advanced so the canonical set wins sync against an older device's copy.
func normalizeStoredTags(tx *sql.Tx) error {
	type tagSet struct {
		modifiedAt string
		tags       []string
	}
	sets := make(map[string]*tagSet)
	var order []string

	rows, err := tx.Query(`
		SELECT tt.task_id, tt.tag, t.modified_at
		FROM task_tags tt
		JOIN todos t ON t.id = tt.task_id
		ORDER BY tt.task_id, tt.tag`)
	if err != nil {
		return fmt.Errorf("scan task tags: %w", err)
	}
	for rows.Next() {
		var taskID, tag, modifiedAt string
		if err := rows.Scan(&taskID, &tag, &modifiedAt); err != nil {
			rows.Close()
			return err
		}
		set := sets[taskID]
		if set == nil {
			set = &tagSet{modifiedAt: modifiedAt}
			sets[taskID] = set
			order = append(order, taskID)
		}
		set.tags = append(set.tags, tag)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	deleteTags, err := tx.Prepare(`DELETE FROM task_tags WHERE task_id = ?`)
	if err != nil {
		return err
	}
	defer deleteTags.Close()
	insertTag, err := tx.Prepare(`INSERT INTO task_tags (task_id, tag) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer insertTag.Close()
	updateTask, err := tx.Prepare(`UPDATE todos SET modified_at = ? WHERE id = ?`)
	if err != nil {
		return err
	}
	defer updateTask.Close()

	for _, taskID := range order {
		set := sets[taskID]
		canonical := make([]string, 0, len(set.tags))
		seen := make(map[string]struct{}, len(set.tags))
		changed := false
		for _, tag := range set.tags {
			normalized := todo.NormalizeTag(tag)
			if normalized != tag {
				changed = true
			}
			if normalized == "" {
				continue
			}
			if _, duplicate := seen[normalized]; duplicate {
				changed = true
				continue
			}
			seen[normalized] = struct{}{}
			canonical = append(canonical, normalized)
		}
		if !changed {
			continue
		}
		if _, err := deleteTags.Exec(taskID); err != nil {
			return err
		}
		for _, tag := range canonical {
			if _, err := insertTag.Exec(taskID, tag); err != nil {
				return err
			}
		}
		stamp := todo.StampModified(parseTime(set.modifiedAt))
		if _, err := updateTask.Exec(fmtTime(stamp), taskID); err != nil {
			return err
		}
	}
	return nil
}
