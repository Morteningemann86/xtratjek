package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
)

func suggestTask(id, title, project, notes string, created time.Time) todo.Todo {
	t := todo.New(title)
	t.ID = id
	t.Project = project
	t.Notes = notes
	t.CreatedAt = created
	return t
}

func TestCollectDepSuggestionsNoteRefs(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	blocker := suggestTask("aaaa1111-0000-0000-0000-000000000000", "Header status line", "",
		"Absorbs the toast task bbbb2222 and the glyph work.", base)
	dependent := suggestTask("bbbb2222-0000-0000-0000-000000000000", "Toast kinds", "", "", base)
	unrelated := suggestTask("cccc3333-0000-0000-0000-000000000000", "Water plants", "", "", base)

	got := collectDepSuggestions([]todo.Todo{blocker, dependent, unrelated})
	if len(got) != 1 {
		t.Fatalf("suggestions = %d, want 1: %+v", len(got), got)
	}
	// Default direction: the mentioned task (a) depends on the mentioner (b).
	if got[0].a.ID != dependent.ID || got[0].b.ID != blocker.ID {
		t.Errorf("suggested %s depends on %s, want %s depends on %s",
			got[0].a.ID[:8], got[0].b.ID[:8], dependent.ID[:8], blocker.ID[:8])
	}
	if !strings.Contains(got[0].evidence, "mention") {
		t.Errorf("evidence = %q, want a note-ref mention", got[0].evidence)
	}
}

func TestCollectDepSuggestionsSkipsLinkedAndKin(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	blocker := suggestTask("aaaa1111-0000-0000-0000-000000000000", "Base", "", "see bbbb2222 and dddd4444", base)
	linked := suggestTask("bbbb2222-0000-0000-0000-000000000000", "Already linked", "", "", base)
	linked.Dependencies = []string{blocker.ID}
	child := suggestTask("dddd4444-0000-0000-0000-000000000000", "Subtask", "", "", base)
	child.ParentID = blocker.ID

	if got := collectDepSuggestions([]todo.Todo{blocker, linked, child}); len(got) != 0 {
		t.Fatalf("suggestions = %+v, want none (already linked / parent-child)", got)
	}
}

func TestCollectDepSuggestionsTitleOverlap(t *testing.T) {
	base := time.Now().Add(-2 * time.Hour)
	earlier := suggestTask("aaaa1111-0000-0000-0000-000000000000",
		"Write TTRPG ruleset chapter", "ttrpg", "", base)
	later := suggestTask("bbbb2222-0000-0000-0000-000000000000",
		"Send TTRPG ruleset chapter to reviewers", "ttrpg", "", base.Add(time.Minute))
	otherProj := suggestTask("cccc3333-0000-0000-0000-000000000000",
		"TTRPG ruleset printing", "print-shop", "", base)

	got := collectDepSuggestions([]todo.Todo{later, earlier, otherProj})
	if len(got) != 1 {
		t.Fatalf("suggestions = %d, want 1 (same-project pair only): %+v", len(got), got)
	}
	// Default direction: later-created depends on earlier-created.
	if got[0].a.ID != later.ID || got[0].b.ID != earlier.ID {
		t.Errorf("suggested %s depends on %s, want %s depends on %s",
			got[0].a.ID[:8], got[0].b.ID[:8], later.ID[:8], earlier.ID[:8])
	}
}

func TestCollectDepSuggestionsIgnoresDoneAndWeakOverlap(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	done := suggestTask("aaaa1111-0000-0000-0000-000000000000", "Ship the release", "app", "", base)
	done.Status = todo.Done
	mentionsDone := suggestTask("bbbb2222-0000-0000-0000-000000000000", "Cleanup", "", "after aaaa1111 shipped", base)
	oneShared := suggestTask("cccc3333-0000-0000-0000-000000000000", "Release notes draft", "app", "", base)
	oneSharedPeer := suggestTask("dddd4444-0000-0000-0000-000000000000", "Release party", "app", "", base.Add(time.Minute))

	if got := collectDepSuggestions([]todo.Todo{done, mentionsDone, oneShared, oneSharedPeer}); len(got) != 0 {
		t.Fatalf("suggestions = %+v, want none (done target, single shared token)", got)
	}
}

func TestSharedTitleTokens(t *testing.T) {
	got := sharedTitleTokens(
		"Migrate the Navidrome library to /srv/data",
		"Repoint Navidrome after the library migration to /srv/data")
	// "navidrome", "library", "data" shared; "srv" is 3 chars, "migrate" vs
	// "migration" don't literally match, "the"/"to" too short or stopworded.
	want := []string{"data", "library", "navidrome"}
	if len(got) != len(want) {
		t.Fatalf("shared = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("shared[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
