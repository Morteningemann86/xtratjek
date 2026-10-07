package main

import (
	"time"

	"github.com/Morteningemann86/xtratjek/rank"
	"github.com/Morteningemann86/xtratjek/todo"
)

// Repository is the persistence port. The app depends on this contract rather
// than on any concrete store: SQLite fulfils it in production (sqliteRepo), an
// in-memory fake fulfils it in tests. This keeps storage details out of the
// domain/UI layer and makes the store swappable (e.g. a future sync adapter).
//
// Save is differential: callers pass only the tasks that changed since the
// last save and the tasks that have been deleted, both of which the in-memory
// Store tracks and hands over with drainDirty. Tombstones map a deleted ID to
// *when it was deleted*, not when the save runs: saves are debounced, so a
// flush-time stamp would order the deletion after edits that actually came
// after it, and it would not be comparable with the stamp an undo of the same
// deletion writes (touchRestored).
type Repository interface {
	Load() ([]todo.Todo, error)
	Save(dirty []*todo.Todo, tombstones map[string]time.Time) error
	// SetRanker hands the repository the ranker it scores the persisted
	// `sequence` column with. Safe to call while a Save is running.
	SetRanker(rank.Ranker)
	// ResyncScores rewrites the persisted `sequence` column for every live
	// row with the current rank.Ranker. Without this, a bias change or
	// passage of time (Age drift) leaves the column stale relative to the
	// in-memory formula — invisible to the TUI (which sorts in memory) but
	// a trap for anything reading the database directly.
	ResyncScores() error
}
