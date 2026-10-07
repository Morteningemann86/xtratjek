package main

import (
	"time"

	"github.com/Morteningemann86/xtratjek/rank"
	"github.com/Morteningemann86/xtratjek/todo"
)

// fakeRepo is an in-memory Repository for tests — no database, no filesystem.
// This is the payoff of the port: model tests build with a fakeRepo instead of
// touching real storage.
type fakeRepo struct{ todos []todo.Todo }

func (r *fakeRepo) Load() ([]todo.Todo, error) { return r.todos, nil }

// ResyncScores is a no-op for the in-memory fake — there's no persisted
// score column to refresh. The contract ("persisted score matches the
// formula") is vacuously satisfied when nothing is persisted.
func (r *fakeRepo) ResyncScores() error { return nil }

func (r *fakeRepo) SetRanker(rank.Ranker) {}

// Save mirrors the whole-snapshot semantics of the SQLite adapter at this step:
// dirty contains the full live set, tombstones is nil. We rebuild r.todos from
// the dirty pointers (deep-copied for test isolation).
func (r *fakeRepo) Save(dirty []*todo.Todo, tombstones map[string]time.Time) error {
	out := make([]todo.Todo, len(dirty))
	for i, p := range dirty {
		out[i] = *p
	}
	r.todos = copyTodos(out)
	return nil
}

// newTestModel builds a model backed by an empty in-memory repo.
func newTestModel() model { return initialModel(&fakeRepo{}) }
