package main

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/Iliorn/tjek/meeting"
)

// openTestStore opens an isolated, throwaway database — the handle-based
// tests below avoid the package-level openStore singleton, like
// TestFileBackedRoundTrip does for todos.
func openTestStore(t *testing.T) *sql.DB {
	t.Helper()
	h, err := openStoreAt(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatalf("openStoreAt: %v", err)
	}
	t.Cleanup(func() { h.Close() })
	return h
}

func TestSaveAndLoadMeeting(t *testing.T) {
	h := openTestStore(t)
	m := meeting.New("Sprint planning")
	m.Transcript = "we discussed the roadmap"
	if err := saveMeetingIn(h, &m); err != nil {
		t.Fatalf("saveMeetingIn: %v", err)
	}

	got, err := loadMeetingsIn(h)
	if err != nil {
		t.Fatalf("loadMeetingsIn: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("loadMeetingsIn() = %d meetings, want 1", len(got))
	}
	if got[0].ID != m.ID || got[0].Title != "Sprint planning" || got[0].Transcript != m.Transcript {
		t.Fatalf("loaded meeting = %+v, want to match %+v", got[0], m)
	}
}

func TestSaveMeetingUpserts(t *testing.T) {
	h := openTestStore(t)
	m := meeting.New("Standup")
	if err := saveMeetingIn(h, &m); err != nil {
		t.Fatal(err)
	}
	m.Summary = "all green"
	m.Status = meeting.StatusReady
	if err := saveMeetingIn(h, &m); err != nil {
		t.Fatal(err)
	}

	got, err := loadMeetingsIn(h)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("loadMeetingsIn() = %d meetings, want 1 (upsert should not duplicate)", len(got))
	}
	if got[0].Summary != "all green" || got[0].Status != meeting.StatusReady {
		t.Fatalf("loaded meeting = %+v, want updated summary/status", got[0])
	}
}

func TestDeleteMeetingSoftHidesIt(t *testing.T) {
	h := openTestStore(t)
	m := meeting.New("Retro")
	if err := saveMeetingIn(h, &m); err != nil {
		t.Fatal(err)
	}
	if err := deleteMeetingSoftIn(h, m.ID); err != nil {
		t.Fatalf("deleteMeetingSoftIn: %v", err)
	}
	got, err := loadMeetingsIn(h)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("loadMeetingsIn() after delete = %v, want empty", got)
	}
}

func TestAttendeesRoundTripThroughStorage(t *testing.T) {
	h := openTestStore(t)
	m := meeting.New("Planning")
	m.SetAttendeesText("Alice, Bob, Carol")
	if err := saveMeetingIn(h, &m); err != nil {
		t.Fatal(err)
	}
	got, err := loadMeetingsIn(h)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Alice", "Bob", "Carol"}
	if len(got[0].Attendees) != len(want) {
		t.Fatalf("Attendees = %v, want %v", got[0].Attendees, want)
	}
	for i := range want {
		if got[0].Attendees[i] != want[i] {
			t.Fatalf("Attendees = %v, want %v", got[0].Attendees, want)
		}
	}
}

func TestReplaceAndLoadSuggestions(t *testing.T) {
	h := openTestStore(t)
	m := meeting.New("Planning")
	if err := saveMeetingIn(h, &m); err != nil {
		t.Fatal(err)
	}

	s1 := meeting.NewSuggestion(m.ID, "Email the vendor")
	s1.SuggestedProject = "Procurement"
	s1.SuggestedTags = []string{"urgent", "vendor"}
	s2 := meeting.NewSuggestion(m.ID, "Update the roadmap doc")

	if err := replaceSuggestionsIn(h, m.ID, []meeting.Suggestion{s1, s2}); err != nil {
		t.Fatalf("replaceSuggestionsIn: %v", err)
	}

	got, err := loadSuggestionsIn(h, m.ID)
	if err != nil {
		t.Fatalf("loadSuggestionsIn: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("loadSuggestionsIn() = %d suggestions, want 2", len(got))
	}
	if got[0].Title != s1.Title || got[0].SuggestedProject != "Procurement" || len(got[0].SuggestedTags) != 2 {
		t.Fatalf("suggestion[0] = %+v", got[0])
	}
	if got[0].Resolved || got[1].Resolved {
		t.Fatal("fresh suggestions should start unresolved")
	}
}

func TestReplaceSuggestionsDiscardsPreviousBatch(t *testing.T) {
	h := openTestStore(t)
	m := meeting.New("Planning")
	if err := saveMeetingIn(h, &m); err != nil {
		t.Fatal(err)
	}
	first := meeting.NewSuggestion(m.ID, "First pass item")
	if err := replaceSuggestionsIn(h, m.ID, []meeting.Suggestion{first}); err != nil {
		t.Fatal(err)
	}
	second := meeting.NewSuggestion(m.ID, "Second pass item")
	if err := replaceSuggestionsIn(h, m.ID, []meeting.Suggestion{second}); err != nil {
		t.Fatal(err)
	}
	got, err := loadSuggestionsIn(h, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "Second pass item" {
		t.Fatalf("loadSuggestionsIn() = %+v, want only the second pass", got)
	}
}

func TestResolveSuggestion(t *testing.T) {
	h := openTestStore(t)
	m := meeting.New("Planning")
	if err := saveMeetingIn(h, &m); err != nil {
		t.Fatal(err)
	}
	s := meeting.NewSuggestion(m.ID, "Do the thing")
	if err := replaceSuggestionsIn(h, m.ID, []meeting.Suggestion{s}); err != nil {
		t.Fatal(err)
	}

	if err := resolveSuggestionIn(h, s.ID, true, "task-123"); err != nil {
		t.Fatalf("resolveSuggestionIn: %v", err)
	}
	got, err := loadSuggestionsIn(h, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got[0].Resolved || !got[0].Accepted || got[0].CreatedTaskID != "task-123" {
		t.Fatalf("resolved suggestion = %+v", got[0])
	}
}

func TestUpdateSuggestionEdits(t *testing.T) {
	h := openTestStore(t)
	m := meeting.New("Planning")
	if err := saveMeetingIn(h, &m); err != nil {
		t.Fatal(err)
	}
	s := meeting.NewSuggestion(m.ID, "Original title")
	if err := replaceSuggestionsIn(h, m.ID, []meeting.Suggestion{s}); err != nil {
		t.Fatal(err)
	}

	s.Title = "Edited title"
	s.SuggestedProject = "New project"
	s.SuggestedTags = []string{"a", "b"}
	s.SuggestedPriority = "h"
	s.SuggestedDue = "friday"
	if err := updateSuggestionEditsIn(h, s); err != nil {
		t.Fatalf("updateSuggestionEditsIn: %v", err)
	}

	got, err := loadSuggestionsIn(h, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Title != "Edited title" || got[0].SuggestedProject != "New project" ||
		got[0].SuggestedPriority != "h" || got[0].SuggestedDue != "friday" || len(got[0].SuggestedTags) != 2 {
		t.Fatalf("edited suggestion = %+v", got[0])
	}
}
