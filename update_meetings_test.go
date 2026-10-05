package main

import (
	"testing"

	"github.com/Iliorn/tjek/aiprovider"
	"github.com/Iliorn/tjek/meeting"
	"github.com/Iliorn/tjek/todo"
)

// update_meetings_test.go scripts the Meetings tab the same way
// update_keyscript_test.go does for the rest of the app: real Update calls,
// asserting the store/review bookkeeping rather than rendering. The record →
// transcribe → summarize pipeline's network calls are simulated by delivering
// the messages those tea.Cmds would produce directly, which is also what
// exercises handleRecordingStopped/handleTranscribeDone/handleAIPassDone
// without a real ffmpeg or API call.

func TestScriptAddMeetingAndRunReview(t *testing.T) {
	m := modelWithTasks(t)
	m.tab = tabMeetings

	// Add.
	m = sendKey(t, m, "a")
	if m.mode != modeAddMeeting {
		t.Fatalf("after 'a' on Meetings: mode = %v, want modeAddMeeting", m.mode)
	}
	m = script(t, m, "Sprint planning", "enter")
	if m.mode != modeNormal {
		t.Fatalf("after enter: mode = %v, want modeNormal", m.mode)
	}
	if len(m.meetings) != 1 {
		t.Fatalf("meetings = %d, want 1", len(m.meetings))
	}
	mt := m.meetings[0]
	if mt.Title != "Sprint planning" || mt.Status != meeting.StatusDraft {
		t.Fatalf("created meeting = %+v", mt)
	}

	// Open it, type a transcript directly (bypassing $EDITOR, which this
	// suite cannot drive), and run the AI pass.
	m = sendKey(t, m, "enter")
	if m.pane != paneDetail || m.openMeetingID != mt.ID {
		t.Fatalf("after enter on the list: pane=%v openMeetingID=%q", m.pane, m.openMeetingID)
	}
	mt.Transcript = "Alice will email the vendor. Bob will update the roadmap doc."
	if err := saveMeeting(mt); err != nil {
		t.Fatal(err)
	}

	m = script(t, m, "g")
	if mt.Status != meeting.StatusSummarizing {
		t.Fatalf("after 'g': meeting status = %v, want StatusSummarizing", mt.Status)
	}

	// Simulate the AI pass completing — two action items.
	next, _ := m.Update(aiPassDoneMsg{
		meetingID: mt.ID,
		summary:   "Alice and Bob have follow-ups.",
		suggestions: []aiprovider.Suggestion{
			{Title: "Email the vendor", Priority: "h"},
			{Title: "Update the roadmap doc", Priority: "m"},
		},
	})
	m = next.(model)
	if mt.Status != meeting.StatusReady {
		t.Fatalf("after AI pass: meeting status = %v, want StatusReady", mt.Status)
	}
	if len(m.meetingSuggestions) != 2 {
		t.Fatalf("meetingSuggestions = %d, want 2", len(m.meetingSuggestions))
	}
	if m.meetingReviewCursor != 0 {
		t.Fatalf("meetingReviewCursor = %d, want 0", m.meetingReviewCursor)
	}

	tasksBefore := m.len()

	// Accept the first.
	m = sendKey(t, m, "y")
	if m.len() != tasksBefore+1 {
		t.Fatalf("after accept: store has %d tasks, want %d", m.len(), tasksBefore+1)
	}
	created := m.currentTodo()
	if created == nil {
		// currentTodo tracks the Tasks-tab cursor, not Meetings' — look it up
		// by title instead.
		for _, tk := range m.allTodos() {
			if tk.Title == "Email the vendor" {
				created = tk
			}
		}
	}
	if created == nil || created.Priority != todo.PriorityHigh || created.MeetingID != mt.ID {
		t.Fatalf("created task = %+v", created)
	}
	if !m.meetingSuggestions[0].Resolved || !m.meetingSuggestions[0].Accepted {
		t.Fatalf("suggestion[0] = %+v, want resolved+accepted", m.meetingSuggestions[0])
	}
	if m.meetingReviewCursor != 1 {
		t.Fatalf("meetingReviewCursor = %d, want 1 (advanced to the next unresolved)", m.meetingReviewCursor)
	}

	// Reject the second — meeting should finish review.
	tasksAfterAccept := m.len()
	m = sendKey(t, m, "x")
	if m.len() != tasksAfterAccept {
		t.Fatalf("reject created a task: store has %d, want %d", m.len(), tasksAfterAccept)
	}
	if !m.meetingSuggestions[1].Resolved || m.meetingSuggestions[1].Accepted {
		t.Fatalf("suggestion[1] = %+v, want resolved, not accepted", m.meetingSuggestions[1])
	}
	if mt.Status != meeting.StatusReviewed {
		t.Fatalf("meeting status = %v, want StatusReviewed once every suggestion is resolved", mt.Status)
	}
	if m.meetingReviewCursor != -1 {
		t.Fatalf("meetingReviewCursor = %d, want -1 once review is finished", m.meetingReviewCursor)
	}
}

func TestScriptDeleteMeetingAsksToConfirm(t *testing.T) {
	m := modelWithTasks(t)
	m.tab = tabMeetings
	m = script(t, m, "a", "Standup", "enter")
	if len(m.meetings) != 1 {
		t.Fatalf("meetings = %d, want 1", len(m.meetings))
	}

	m = sendKey(t, m, "x")
	if m.mode != modeConfirm {
		t.Fatalf("after 'x' on the list: mode = %v, want modeConfirm", m.mode)
	}
	m = sendKey(t, m, "y")
	if len(m.meetings) != 0 {
		t.Fatalf("after confirming delete: meetings = %d, want 0", len(m.meetings))
	}
}
