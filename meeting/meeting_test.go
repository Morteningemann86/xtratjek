package meeting

import "testing"

func TestNewMeetingIsDraft(t *testing.T) {
	m := New("Sprint planning")
	if m.Status != StatusDraft {
		t.Fatalf("New() status = %q, want %q", m.Status, StatusDraft)
	}
	if m.ID == "" {
		t.Fatal("New() left ID empty")
	}
	if m.CreatedAt.IsZero() || m.ModifiedAt.IsZero() {
		t.Fatal("New() left timestamps zero")
	}
}

func TestAttendeesTextRoundTrip(t *testing.T) {
	var m Meeting
	m.SetAttendeesText("Alice,  Bob ,, Carol")
	want := []string{"Alice", "Bob", "Carol"}
	if len(m.Attendees) != len(want) {
		t.Fatalf("Attendees = %v, want %v", m.Attendees, want)
	}
	for i := range want {
		if m.Attendees[i] != want[i] {
			t.Fatalf("Attendees[%d] = %q, want %q", i, m.Attendees[i], want[i])
		}
	}
	if got := m.AttendeesText(); got != "Alice, Bob, Carol" {
		t.Fatalf("AttendeesText() = %q", got)
	}
}

func TestSetAttendeesTextEmpty(t *testing.T) {
	m := Meeting{Attendees: []string{"Alice"}}
	m.SetAttendeesText("   ,  ")
	if len(m.Attendees) != 0 {
		t.Fatalf("Attendees = %v, want empty", m.Attendees)
	}
}

func TestCanRunAI(t *testing.T) {
	cases := []struct {
		status     Status
		transcript string
		want       bool
	}{
		{StatusDraft, "", false},
		{StatusDraft, "some notes", true},
		{StatusRecording, "some notes", false},
		{StatusTranscribing, "some notes", false},
		{StatusSummarizing, "some notes", false},
		{StatusReady, "some notes", true},
		{StatusError, "some notes", true},
	}
	for _, c := range cases {
		m := Meeting{Status: c.status, Transcript: c.transcript}
		if got := m.CanRunAI(); got != c.want {
			t.Errorf("CanRunAI() status=%s transcript=%q = %v, want %v", c.status, c.transcript, got, c.want)
		}
	}
}

func TestCanRecord(t *testing.T) {
	for _, s := range []Status{StatusDraft, StatusReady, StatusReviewed, StatusError} {
		if !(Meeting{Status: s}).CanRecord() {
			t.Errorf("CanRecord() status=%s = false, want true", s)
		}
	}
	for _, s := range []Status{StatusRecording, StatusTranscribing, StatusSummarizing} {
		if (Meeting{Status: s}).CanRecord() {
			t.Errorf("CanRecord() status=%s = true, want false", s)
		}
	}
}

func TestNeedsReview(t *testing.T) {
	if !(Meeting{Status: StatusReady}).NeedsReview() {
		t.Error("NeedsReview() = false for StatusReady, want true")
	}
	if (Meeting{Status: StatusReviewed}).NeedsReview() {
		t.Error("NeedsReview() = true for StatusReviewed, want false")
	}
}

func TestNewSuggestionDefaults(t *testing.T) {
	s := NewSuggestion("meeting-1", "Send the report")
	if s.ID == "" {
		t.Fatal("NewSuggestion() left ID empty")
	}
	if s.MeetingID != "meeting-1" || s.Title != "Send the report" {
		t.Fatalf("NewSuggestion() = %+v", s)
	}
	if s.SuggestedPriority != "m" {
		t.Fatalf("SuggestedPriority = %q, want \"m\"", s.SuggestedPriority)
	}
	if s.Resolved || s.Accepted {
		t.Fatal("NewSuggestion() should start unresolved")
	}
}
