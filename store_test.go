package main

import (
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
)

// A reload that carries the task versions the Store already has must be
// recognised as a no-op: the watcher fires on our own writes, and rebuilding
// the Store for those costs milliseconds on the event loop.
func TestSameAsLoadedRecognisesAnUnchangedSnapshot(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	a := todo.New("alpha")
	a.ID, a.ModifiedAt = "a", base
	b := todo.New("beta")
	b.ID, b.ModifiedAt = "b", base.Add(time.Minute)

	var s Store
	s.ensureTasks()
	s.add(a)
	s.add(b)

	// Same versions, reversed order — the load order is not part of identity.
	if !s.sameAsLoaded([]todo.Todo{b, a}) {
		t.Error("an identical snapshot was reported as changed")
	}
	if s.sameAsLoaded([]todo.Todo{a}) {
		t.Error("a snapshot missing a task was reported as unchanged")
	}
	c := todo.New("gamma")
	c.ID, c.ModifiedAt = "c", base
	if s.sameAsLoaded([]todo.Todo{a, b, c}) {
		t.Error("a snapshot with an extra task was reported as unchanged")
	}
	// An edit elsewhere bumps ModifiedAt, which is what makes it visible here.
	edited := b
	edited.Title = "beta, edited"
	edited.ModifiedAt = base.Add(2 * time.Minute)
	if s.sameAsLoaded([]todo.Todo{a, edited}) {
		t.Error("an edited task was reported as unchanged")
	}
	// A tombstone arriving for a live task is a change too.
	dead := a
	dead.DeletedAt = time.Now()
	if s.sameAsLoaded([]todo.Todo{dead, b}) {
		t.Error("a tombstone was reported as unchanged")
	}
}
