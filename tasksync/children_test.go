package tasksync

import (
	"reflect"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
)

// children_test.go covers the child collections — the comment fold and the
// digest that decides whether a sync changed anything.

func comment(id, text string, modified time.Time) todo.Comment {
	return todo.Comment{ID: id, Text: text, CreatedAt: modified, ModifiedAt: modified}
}

func TestMergeComments(t *testing.T) {
	base := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)
	later := base.Add(time.Hour)

	t.Run("union by id, both sides kept", func(t *testing.T) {
		a := []todo.Comment{comment("1", "from the laptop", base)}
		b := []todo.Comment{comment("2", "from the phone", base)}
		got := mergeComments(a, b)
		if len(got) != 2 {
			t.Fatalf("merged %d comments, want both", len(got))
		}
	})

	t.Run("the later edit of the same comment wins", func(t *testing.T) {
		a := []todo.Comment{comment("1", "first draft", base)}
		b := []todo.Comment{comment("1", "reworded", later)}
		if got := mergeComments(a, b); got[0].Text != "reworded" {
			t.Errorf("a,b merged to %q, want the later edit", got[0].Text)
		}
		if got := mergeComments(b, a); got[0].Text != "reworded" {
			t.Errorf("b,a merged to %q — the fold is not commutative", got[0].Text)
		}
	})

	t.Run("a tombstone beats a live version and keeps propagating", func(t *testing.T) {
		live := comment("1", "still here", later)
		tomb := comment("1", "gone", base)
		tomb.DeletedAt = base
		for _, got := range [][]todo.Comment{
			mergeComments([]todo.Comment{live}, []todo.Comment{tomb}),
			mergeComments([]todo.Comment{tomb}, []todo.Comment{live}),
		} {
			if len(got) != 1 || got[0].DeletedAt.IsZero() {
				t.Fatalf("merged to %d comments, want the tombstone alone", len(got))
			}
		}
	})

	t.Run("an exact tie resolves the same way from both sides", func(t *testing.T) {
		a := []todo.Comment{comment("1", "version a", base)}
		b := []todo.Comment{comment("1", "version b", base)}
		if !reflect.DeepEqual(mergeComments(a, b), mergeComments(b, a)) {
			t.Error("a tie resolved differently depending on argument order")
		}
	})

	t.Run("nil sides", func(t *testing.T) {
		if got := mergeComments(nil, nil); len(got) != 0 {
			t.Errorf("merging two nils produced %d", len(got))
		}
		only := []todo.Comment{comment("1", "solo", base)}
		if got := mergeComments(nil, only); len(got) != 1 {
			t.Errorf("merging against nil dropped the comment")
		}
	})
}

// A whole-task merge has to carry the children through, not just the scalars.
func TestMergeCarriesCommentsAcrossDevices(t *testing.T) {
	base := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)
	server := todo.New("write the postmortem")
	server.ID = "a"
	server.ModifiedAt = base
	server.Comments = []todo.Comment{comment("1", "check the disk first", base)}

	client := server
	client.Comments = []todo.Comment{comment("2", "and the clock", base)}

	merged := Merge([]todo.Todo{server}, []todo.Todo{client})
	if len(merged) != 1 {
		t.Fatalf("merged to %d tasks", len(merged))
	}
	if len(merged[0].Comments) != 2 {
		t.Errorf("task kept %d comments, want both devices'", len(merged[0].Comments))
	}
}

// The digest decides whether a sync "changed" anything, which gates both the
// write and the broadcast. It has to ignore orderings the merge may introduce
// and the zone a load happened to rehydrate in.
func TestStoreDigestIgnoresOrderAndZone(t *testing.T) {
	base := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)
	task := todo.New("digest me")
	task.ID = "a"
	task.ModifiedAt = base
	task.Tags = []string{"work", "home"}
	task.Dependencies = []string{"b", "c"}
	task.Comments = []todo.Comment{{ID: "2", Text: "second", CreatedAt: base}, {ID: "1", Text: "first", CreatedAt: base}}
	task.TimeEntries = []todo.TimeEntry{{ID: "2", StartedAt: base}, {ID: "1", StartedAt: base}}

	shuffled := task
	shuffled.Tags = []string{"home", "work"}
	shuffled.Dependencies = []string{"c", "b"}
	shuffled.Comments = []todo.Comment{task.Comments[1], task.Comments[0]}
	shuffled.TimeEntries = []todo.TimeEntry{task.TimeEntries[1], task.TimeEntries[0]}

	if StoreDigest([]todo.Todo{task}) != StoreDigest([]todo.Todo{shuffled}) {
		t.Error("reordering children changed the digest — every sync would look like a change")
	}

	// Same instants, different zone: a store loaded in another timezone must
	// hash the same, or two devices would write to each other forever.
	elsewhere := task
	zone := time.FixedZone("UTC+7", 7*3600)
	elsewhere.ModifiedAt = task.ModifiedAt.In(zone)
	elsewhere.CreatedAt = task.CreatedAt.In(zone)
	if StoreDigest([]todo.Todo{task}) != StoreDigest([]todo.Todo{elsewhere}) {
		t.Error("the same instant in another zone changed the digest")
	}

	// And a real change must still register.
	changed := task
	changed.Title = "something else"
	if StoreDigest([]todo.Todo{task}) == StoreDigest([]todo.Todo{changed}) {
		t.Error("an edited title did not change the digest")
	}

	// Canonicalization must not scribble on the caller's slices.
	before := append([]string(nil), task.Tags...)
	_ = CanonicalJSON(task)
	if !reflect.DeepEqual(task.Tags, before) {
		t.Errorf("CanonicalJSON reordered the caller's tags: %v", task.Tags)
	}
}
