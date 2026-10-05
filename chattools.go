package main

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/Iliorn/tjek/aiprovider"
	"github.com/Iliorn/tjek/meeting"
	"github.com/Iliorn/tjek/todo"
)

// chattools.go defines the Chat tab's tool set: what the model is offered
// (chatToolSpecs) and how the read-only ones are executed (runReadTool).
// create_task/complete_task/edit_task are declared here too — the model
// needs their schemas to call them — but actually applying one happens in
// chatops.go/update_chat.go after the user confirms, never here; see
// isActionTool and ARCHITECTURE.md's notes on the Chat tab for why that
// split exists (chatReplyCmd must never mutate model state from its
// goroutine).

// chatSnapshot is the plain-value copy of the user's data a chat reply is
// computed against — never live *todo.Todo/*meeting.Meeting pointers into
// model.Store, which chatReplyCmd's goroutine has no business touching
// concurrently with the Update loop. Built once in updateChat when a
// message is sent; stays fixed for that whole reply even if the user is
// still around to change something mid-reply.
type chatSnapshot struct {
	todos    []todo.Todo
	meetings []meeting.Meeting
	projects []string
}

const (
	toolListProjects = "list_projects"
	toolListTasks    = "list_tasks"
	toolGetTask      = "get_task"
	toolListMeetings = "list_meetings"
	toolGetMeeting   = "get_meeting"
	toolCreateTask   = "create_task"
	toolCompleteTask = "complete_task"
	toolEditTask     = "edit_task"
)

// isActionTool reports whether name mutates data — these are never run by
// chatReplyCmd's loop, only proposed for the user to confirm (see
// firstActionCall in chatops.go).
func isActionTool(name string) bool {
	switch name {
	case toolCreateTask, toolCompleteTask, toolEditTask:
		return true
	default:
		return false
	}
}

// chatToolSpecs is every tool offered to the model, read and action alike —
// chatSystemPrompt tells it the action ones are safe to call directly
// because the app confirms before anything runs.
var chatToolSpecs = []aiprovider.ToolSpec{
	{
		Name:        toolListProjects,
		Description: "List every project name currently in use.",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	},
	{
		Name:        toolListTasks,
		Description: "List tasks, optionally filtered. Returns compact rows (id, title, project, tags, priority, due, done) — call get_task for full detail on one.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project":      map[string]any{"type": "string", "description": "exact project name to filter to"},
				"status":       map[string]any{"type": "string", "enum": []string{"open", "done"}, "description": "filter to open or done tasks; omit for both"},
				"tag":          map[string]any{"type": "string", "description": "filter to tasks with this tag"},
				"overdue_only": map[string]any{"type": "boolean", "description": "only tasks past their due date and not done"},
			},
		},
	},
	{
		Name:        toolGetTask,
		Description: "Get one task's full detail, including notes and comments.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"id": map[string]any{"type": "string"}},
			"required":   []string{"id"},
		},
	},
	{
		Name:        toolListMeetings,
		Description: "List meetings, optionally filtered by status. Returns compact rows (id, title, date, status) — call get_meeting for the summary/notes/transcript.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"status": map[string]any{"type": "string", "description": "e.g. draft, ready, reviewed — omit for all"},
			},
		},
	},
	{
		Name:        toolGetMeeting,
		Description: "Get one meeting's attendees, notes, transcript, and AI summary.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"id": map[string]any{"type": "string"}},
			"required":   []string{"id"},
		},
	},
	{
		Name:        toolCreateTask,
		Description: "Propose creating a new task. The app will ask the user to confirm before it is actually created.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title":    map[string]any{"type": "string"},
				"project":  map[string]any{"type": "string", "description": "an existing project name, or omit"},
				"tags":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"priority": map[string]any{"type": "string", "enum": []string{"h", "m", "l"}},
				"due":      map[string]any{"type": "string", "description": "free text, e.g. \"friday\", \"+3d\", \"2026-10-20\""},
			},
			"required": []string{"title"},
		},
	},
	{
		Name:        toolCompleteTask,
		Description: "Propose marking a task done. The app will ask the user to confirm first.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"id": map[string]any{"type": "string", "description": "the task's id, from list_tasks/get_task"}},
			"required":   []string{"id"},
		},
	},
	{
		Name:        toolEditTask,
		Description: "Propose changing a task's title, project, tags, priority, or due date. The app will ask the user to confirm first. Omit any field that should stay the same.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":       map[string]any{"type": "string"},
				"title":    map[string]any{"type": "string"},
				"project":  map[string]any{"type": "string"},
				"tags":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"priority": map[string]any{"type": "string", "enum": []string{"h", "m", "l"}},
				"due":      map[string]any{"type": "string"},
			},
			"required": []string{"id"},
		},
	},
}

// maxChatToolResultChars caps one tool result's JSON — mainly a backstop
// against get_meeting on an exceptionally long transcript. Keeps a single
// lookup bounded regardless of how big one record is, the same spirit as
// aiprovider.maxTranscriptChars.
const maxChatToolResultChars = 20000

func truncateChatResult(s string) string {
	if len(s) <= maxChatToolResultChars {
		return s
	}
	return s[:maxChatToolResultChars] + tr("…[truncated]")
}

func argString(args map[string]any, key string) string {
	s, _ := args[key].(string)
	return s
}

func argBool(args map[string]any, key string) bool {
	b, _ := args[key].(bool)
	return b
}

func toolErrorJSON(msg string) string {
	b, _ := json.Marshal(map[string]string{"error": msg})
	return string(b)
}

// ── Read tool rows ───────────────────────────────────────────────────────────

type chatTaskRow struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Project  string   `json:"project,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Priority string   `json:"priority"`
	Due      string   `json:"due,omitempty"`
	Done     bool     `json:"done"`
}

func chatTaskRowFrom(t todo.Todo) chatTaskRow {
	row := chatTaskRow{
		ID:       t.ID,
		Title:    t.Title,
		Project:  t.Project,
		Tags:     t.Tags,
		Priority: chatPriorityLetter(t.Priority),
		Done:     t.Status == todo.Done,
	}
	if !t.DueDate.IsZero() {
		row.Due = t.DueDate.Format("2006-01-02")
	}
	return row
}

// chatPriorityLetter is lowercase, matching the quick-add grammar and
// meeting.Suggestion's priority convention (acceptSuggestion) — cli.go's
// own priorityLetter is uppercase for CLI output and serves a different
// convention, not reused here.
func chatPriorityLetter(p todo.Priority) string {
	switch p {
	case todo.PriorityHigh:
		return "h"
	case todo.PriorityLow:
		return "l"
	default:
		return "m"
	}
}

type chatTaskDetail struct {
	chatTaskRow
	Notes    string   `json:"notes,omitempty"`
	Comments []string `json:"comments,omitempty"`
}

type chatMeetingRow struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Date   string `json:"date"`
	Status string `json:"status"`
}

func chatMeetingRowFrom(mt meeting.Meeting) chatMeetingRow {
	return chatMeetingRow{ID: mt.ID, Title: mt.Title, Date: mt.Date.Format("2006-01-02"), Status: string(mt.Status)}
}

type chatMeetingDetail struct {
	chatMeetingRow
	Attendees  []string `json:"attendees,omitempty"`
	Notes      string   `json:"notes,omitempty"`
	Transcript string   `json:"transcript,omitempty"`
	Summary    string   `json:"summary,omitempty"`
}

// ── Execution ────────────────────────────────────────────────────────────────

// runReadTool executes one of the read-only tools (never create/complete/
// edit_task — see isActionTool) against snap and returns its JSON result,
// or a JSON {"error": "..."} object for a bad/unknown call. Never panics on
// malformed arguments: a chat tool call is model output, not trusted input,
// and a bad call should read back as "that didn't work", not crash the app.
func runReadTool(call aiprovider.ToolCall, snap chatSnapshot, now time.Time) string {
	switch call.Name {
	case toolListProjects:
		return mustJSON(snap.projects)

	case toolListTasks:
		project := argString(call.Arguments, "project")
		status := argString(call.Arguments, "status")
		tag := strings.ToLower(argString(call.Arguments, "tag"))
		overdueOnly := argBool(call.Arguments, "overdue_only")
		rows := []chatTaskRow{}
		for _, t := range snap.todos {
			if project != "" && !strings.EqualFold(t.Project, project) {
				continue
			}
			if status == "open" && t.Status == todo.Done {
				continue
			}
			if status == "done" && t.Status != todo.Done {
				continue
			}
			if tag != "" && !hasTagFold(t.Tags, tag) {
				continue
			}
			if overdueOnly && !t.IsOverdueAt(now) {
				continue
			}
			rows = append(rows, chatTaskRowFrom(t))
		}
		return mustJSON(rows)

	case toolGetTask:
		id := argString(call.Arguments, "id")
		for _, t := range snap.todos {
			if t.ID != id {
				continue
			}
			detail := chatTaskDetail{chatTaskRow: chatTaskRowFrom(t), Notes: t.Notes}
			for _, c := range t.Comments {
				detail.Comments = append(detail.Comments, c.Text)
			}
			return truncateChatResult(mustJSON(detail))
		}
		return toolErrorJSON("no task with that id")

	case toolListMeetings:
		status := argString(call.Arguments, "status")
		rows := []chatMeetingRow{}
		for _, mt := range snap.meetings {
			if status != "" && !strings.EqualFold(string(mt.Status), status) {
				continue
			}
			rows = append(rows, chatMeetingRowFrom(mt))
		}
		return mustJSON(rows)

	case toolGetMeeting:
		id := argString(call.Arguments, "id")
		for _, mt := range snap.meetings {
			if mt.ID != id {
				continue
			}
			detail := chatMeetingDetail{
				chatMeetingRow: chatMeetingRowFrom(mt),
				Attendees:      mt.Attendees,
				Notes:          mt.Notes,
				Transcript:     mt.Transcript,
				Summary:        mt.Summary,
			}
			return truncateChatResult(mustJSON(detail))
		}
		return toolErrorJSON("no meeting with that id")

	default:
		return toolErrorJSON("unknown tool: " + call.Name)
	}
}

func hasTagFold(tags []string, want string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, want) {
			return true
		}
	}
	return false
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return toolErrorJSON(err.Error())
	}
	return string(b)
}
