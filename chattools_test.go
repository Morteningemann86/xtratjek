package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/aiprovider"
	"github.com/Morteningemann86/xtratjek/meeting"
	"github.com/Morteningemann86/xtratjek/todo"
)

func testChatSnapshot() chatSnapshot {
	t1 := todo.New("Ship report")
	t1.ID = "t1"
	t1.Project = "Work"
	t1.AddTag("urgent")
	t1.Priority = todo.PriorityHigh
	t1.DueDate = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	t2 := todo.New("Buy milk")
	t2.ID = "t2"
	t2.Project = "Home"
	t2.Status = todo.Done

	mt := meeting.New("Sprint planning")
	mt.ID = "m1"
	mt.Summary = "Discussed the roadmap."
	mt.Status = meeting.StatusReady

	return chatSnapshot{
		todos:    []todo.Todo{t1, t2},
		meetings: []meeting.Meeting{mt},
		projects: []string{"Work", "Home"},
	}
}

func decodeRows[T any](t *testing.T, result string) []T {
	t.Helper()
	var out []T
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("decoding result %q: %v", result, err)
	}
	return out
}

func TestRunReadToolListProjects(t *testing.T) {
	got := decodeRows[string](t, runReadTool(aiprovider.ToolCall{Name: toolListProjects}, testChatSnapshot(), time.Now()))
	if len(got) != 2 {
		t.Fatalf("list_projects = %v", got)
	}
}

func TestRunReadToolListTasksFilters(t *testing.T) {
	snap := testChatSnapshot()
	rows := decodeRows[chatTaskRow](t, runReadTool(aiprovider.ToolCall{Name: toolListTasks, Arguments: map[string]any{"project": "Work"}}, snap, time.Now()))
	if len(rows) != 1 || rows[0].ID != "t1" {
		t.Fatalf("list_tasks(project=Work) = %+v", rows)
	}

	rows = decodeRows[chatTaskRow](t, runReadTool(aiprovider.ToolCall{Name: toolListTasks, Arguments: map[string]any{"status": "done"}}, snap, time.Now()))
	if len(rows) != 1 || rows[0].ID != "t2" {
		t.Fatalf("list_tasks(status=done) = %+v", rows)
	}

	rows = decodeRows[chatTaskRow](t, runReadTool(aiprovider.ToolCall{Name: toolListTasks, Arguments: map[string]any{"tag": "urgent"}}, snap, time.Now()))
	if len(rows) != 1 || rows[0].ID != "t1" {
		t.Fatalf("list_tasks(tag=urgent) = %+v", rows)
	}
}

func TestRunReadToolListTasksOverdueOnly(t *testing.T) {
	snap := testChatSnapshot() // t1 is due 2026-01-01, t2 has no due date
	rows := decodeRows[chatTaskRow](t, runReadTool(
		aiprovider.ToolCall{Name: toolListTasks, Arguments: map[string]any{"overdue_only": true}},
		snap, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	))
	if len(rows) != 1 || rows[0].ID != "t1" {
		t.Fatalf("list_tasks(overdue_only) = %+v", rows)
	}
}

func TestRunReadToolGetTask(t *testing.T) {
	snap := testChatSnapshot()
	result := runReadTool(aiprovider.ToolCall{Name: toolGetTask, Arguments: map[string]any{"id": "t1"}}, snap, time.Now())
	var detail chatTaskDetail
	if err := json.Unmarshal([]byte(result), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Title != "Ship report" || detail.Project != "Work" {
		t.Fatalf("get_task(t1) = %+v", detail)
	}
}

func TestRunReadToolGetTaskUnknownID(t *testing.T) {
	result := runReadTool(aiprovider.ToolCall{Name: toolGetTask, Arguments: map[string]any{"id": "nope"}}, testChatSnapshot(), time.Now())
	var errResp map[string]string
	if err := json.Unmarshal([]byte(result), &errResp); err != nil {
		t.Fatal(err)
	}
	if errResp["error"] == "" {
		t.Fatalf("expected an error object, got %q", result)
	}
}

func TestRunReadToolGetMeeting(t *testing.T) {
	result := runReadTool(aiprovider.ToolCall{Name: toolGetMeeting, Arguments: map[string]any{"id": "m1"}}, testChatSnapshot(), time.Now())
	var detail chatMeetingDetail
	if err := json.Unmarshal([]byte(result), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Summary != "Discussed the roadmap." {
		t.Fatalf("get_meeting(m1) = %+v", detail)
	}
}

func TestRunReadToolUnknownName(t *testing.T) {
	result := runReadTool(aiprovider.ToolCall{Name: "delete_everything"}, testChatSnapshot(), time.Now())
	var errResp map[string]string
	if err := json.Unmarshal([]byte(result), &errResp); err != nil {
		t.Fatal(err)
	}
	if errResp["error"] == "" {
		t.Fatalf("expected an error object for an unknown tool, got %q", result)
	}
}

func TestIsActionTool(t *testing.T) {
	for _, name := range []string{toolCreateTask, toolCompleteTask, toolEditTask} {
		if !isActionTool(name) {
			t.Errorf("isActionTool(%q) = false, want true", name)
		}
	}
	for _, name := range []string{toolListTasks, toolGetTask, toolListMeetings, toolGetMeeting, toolListProjects} {
		if isActionTool(name) {
			t.Errorf("isActionTool(%q) = true, want false", name)
		}
	}
}

func TestFirstActionCall(t *testing.T) {
	calls := []aiprovider.ToolCall{{Name: toolListTasks}, {Name: toolCompleteTask, ID: "x"}}
	got, ok := firstActionCall(calls)
	if !ok || got.ID != "x" {
		t.Fatalf("firstActionCall() = %+v, %v", got, ok)
	}
	if _, ok := firstActionCall([]aiprovider.ToolCall{{Name: toolListTasks}}); ok {
		t.Fatal("firstActionCall() found an action call among read-only calls")
	}
}

func TestBuildCreateTaskFromArgs(t *testing.T) {
	got := buildCreateTaskFromArgs(map[string]any{
		"title": "Ship report", "project": "Work", "tags": []any{"urgent", "q3"}, "priority": "h",
	})
	if got.Title != "Ship report" || got.Project != "Work" || got.Priority != todo.PriorityHigh {
		t.Fatalf("buildCreateTaskFromArgs() = %+v", got)
	}
	if len(got.Tags) != 2 {
		t.Fatalf("Tags = %v", got.Tags)
	}
}

func TestApplyEditTaskArgsOnlySetFields(t *testing.T) {
	task := todo.New("Original title")
	task.Project = "Home"
	applyEditTaskArgs(&task, map[string]any{"priority": "h"})
	if task.Title != "Original title" || task.Project != "Home" {
		t.Fatalf("applyEditTaskArgs() changed an omitted field: %+v", task)
	}
	if task.Priority != todo.PriorityHigh {
		t.Fatalf("applyEditTaskArgs() priority = %v, want high", task.Priority)
	}
}

func TestDescribeChatAction(t *testing.T) {
	snap := testChatSnapshot()
	got := describeChatAction(aiprovider.ToolCall{Name: toolCreateTask, Arguments: map[string]any{"title": "New task", "project": "Work"}}, snap)
	if got == "" {
		t.Fatal("describeChatAction() returned empty string")
	}
	got = describeChatAction(aiprovider.ToolCall{Name: toolCompleteTask, Arguments: map[string]any{"id": "t1"}}, snap)
	if got == "" {
		t.Fatal("describeChatAction() returned empty string for complete_task")
	}
}
