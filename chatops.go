package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Morteningemann86/xtratjek/aiprovider"
	"github.com/Morteningemann86/xtratjek/meeting"
	"github.com/Morteningemann86/xtratjek/todo"

	tea "github.com/charmbracelet/bubbletea"
)

// chatops.go is the Chat tab's reply loop: turning the persisted, plain-text
// conversation (storage_chat.go) plus a new user message into a model
// response, by way of however many tool calls the model asks for along the
// way. See chattools.go for the tool set and read-tool execution, and
// update_chat.go for how a pendingAction actually gets applied once the
// user confirms — that step needs *model (m.add/m.get/m.pushUndo/
// m.markModified) and so cannot live in the tea.Cmd closure below, which
// runs on its own goroutine and must only ever touch the chatSnapshot it
// was handed.

// chatReplyMsg is chatReplyCmd's result: either Text (the model answered),
// a PendingAction (the model wants to create/complete/edit a task — see
// isActionTool — and the user must confirm before anything happens), or Err.
// Exactly one is set.
type chatReplyMsg struct {
	text          string
	pendingAction *aiprovider.ToolCall
	err           error
	epoch         int // the model's chatEpoch when the request started
}

// maxChatToolHops bounds how many read-tool round trips one reply can make
// before giving up — a guard against a model that keeps calling tools
// without ever answering, not a limit anyone should expect to hit with the
// five-ish tools this app offers.
const maxChatToolHops = 6

var errTooManyToolHops = errors.New("the assistant kept calling tools without answering — try rephrasing your question")

// buildChatSnapshot copies the model's current tasks/meetings/projects by
// value — see chatSnapshot's doc comment (chattools.go) for why this must
// never hand out the live *todo.Todo/*meeting.Meeting pointers model.Store
// and model.meetings otherwise use.
func buildChatSnapshot(m *model) chatSnapshot {
	live := m.allTodos()
	snap := chatSnapshot{
		todos:    make([]todo.Todo, len(live)),
		meetings: make([]meeting.Meeting, len(m.meetings)),
		projects: m.allProjectsForList(),
	}
	for i, t := range live {
		snap.todos[i] = *t
	}
	for i, mt := range m.meetings {
		snap.meetings[i] = *mt
	}
	return snap
}

// chatHistoryToTurns converts the persisted conversation to the plain
// user/assistant turns a fresh reply starts from. Tool-call scaffolding is
// never part of this: it is built fresh, hop by hop, inside chatReplyCmd
// each time, and discarded once that call returns (see the package doc
// comment above).
func chatHistoryToTurns(msgs []chatMessage) []aiprovider.Turn {
	turns := make([]aiprovider.Turn, len(msgs))
	for i, m := range msgs {
		turns[i] = aiprovider.Turn{Role: m.Role, Content: m.Content}
	}
	return turns
}

// firstActionCall returns the first tool call in calls that mutates data
// (see isActionTool), if any. chatReplyCmd's loop stops there rather than
// running it — see that function's comment on why no second model call is
// needed to resume once the user answers.
func firstActionCall(calls []aiprovider.ToolCall) (aiprovider.ToolCall, bool) {
	for _, c := range calls {
		if isActionTool(c.Name) {
			return c, true
		}
	}
	return aiprovider.ToolCall{}, false
}

// chatReplyCmd drives one reply: call the provider, and if it asks for
// tools, either run them locally (read-only ones) and call again, or stop
// and surface the first action call for confirmation. snap is a plain-value
// snapshot of the user's data taken before this Cmd was built — the
// goroutine this runs on must never read model.Store directly, since the
// Update loop can be mutating it concurrently. The reply carries epoch
// back so handleChatReply can drop one that outlived a reset.
func chatReplyCmd(epoch int, history []aiprovider.Turn, snap chatSnapshot, provider string, keys aiprovider.Keys) tea.Cmd {
	return func() tea.Msg {
		msg := chatReply(history, snap, provider, keys)
		msg.epoch = epoch
		return msg
	}
}

func chatReply(history []aiprovider.Turn, snap chatSnapshot, provider string, keys aiprovider.Keys) chatReplyMsg {
	p, err := aiprovider.New(provider, keys)
	if err != nil {
		return chatReplyMsg{err: err}
	}
	ctx, cancel := context.WithTimeout(context.Background(), aiRequestTimeout)
	defer cancel()
	system := aiprovider.ChatSystemPrompt(time.Now().Format("2006-01-02"))
	for hop := 0; hop < maxChatToolHops; hop++ {
		result, err := p.Chat(ctx, system, history, chatToolSpecs)
		if err != nil {
			return chatReplyMsg{err: err}
		}
		if len(result.ToolCalls) == 0 {
			return chatReplyMsg{text: result.Text}
		}
		history = append(history, aiprovider.Turn{Role: "assistant", ToolCalls: result.ToolCalls})
		if call, ok := firstActionCall(result.ToolCalls); ok {
			return chatReplyMsg{pendingAction: &call}
		}
		for _, call := range result.ToolCalls {
			history = append(history, aiprovider.Turn{
				Role: "tool", ToolCallID: call.ID, Content: runReadTool(call, snap, time.Now()),
			})
		}
	}
	return chatReplyMsg{err: errTooManyToolHops}
}

// ── Describing a pending action for the y/n prompt ──────────────────────────

// describeChatAction renders call as the confirmation line's subject
// ("Create task \"Ship report\" (project: Work, due: friday)") — pure text,
// no mutation; update_chat.go wraps it in the actual "… — y/n" prompt.
func describeChatAction(call aiprovider.ToolCall, snap chatSnapshot) string {
	switch call.Name {
	case toolCreateTask:
		return fmt.Sprintf(tr("Create task %q%s"), argString(call.Arguments, "title"), chatActionFieldsSuffix(call.Arguments))
	case toolCompleteTask:
		return fmt.Sprintf(tr("Mark %q done"), chatTaskTitleFor(call, snap))
	case toolEditTask:
		return fmt.Sprintf(tr("Edit %q%s"), chatTaskTitleFor(call, snap), chatActionFieldsSuffix(call.Arguments))
	default:
		return call.Name
	}
}

func chatTaskTitleFor(call aiprovider.ToolCall, snap chatSnapshot) string {
	id := argString(call.Arguments, "id")
	for _, t := range snap.todos {
		if t.ID == id {
			return t.Title
		}
	}
	return id
}

func chatActionFieldsSuffix(args map[string]any) string {
	var parts []string
	if p := argString(args, "project"); p != "" {
		parts = append(parts, tr("project")+": "+p)
	}
	if d := argString(args, "due"); d != "" {
		parts = append(parts, tr("due")+": "+d)
	}
	if pr := argString(args, "priority"); pr != "" {
		parts = append(parts, tr("priority")+": "+pr)
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// ── Building the mutation itself ────────────────────────────────────────────

// chatArgTags reads a "tags" argument (a []any of strings, per how
// encoding/json decodes a JSON array into map[string]any) into []string,
// ignoring anything that isn't a string rather than failing the whole call
// over one bad element — tool arguments are model output, not trusted input.
func chatArgTags(args map[string]any) []string {
	raw, ok := args["tags"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func chatArgPriority(args map[string]any) (todo.Priority, bool) {
	switch argString(args, "priority") {
	case "h":
		return todo.PriorityHigh, true
	case "m":
		return todo.PriorityMedium, true
	case "l":
		return todo.PriorityLow, true
	default:
		return 0, false
	}
}

// buildCreateTaskFromArgs mirrors acceptSuggestion's field-by-field mapping
// (meetingops.go) — a chat-proposed task is built the same way an accepted
// meeting suggestion is, just reading a tool call's arguments instead of a
// meeting.Suggestion.
func buildCreateTaskFromArgs(args map[string]any) todo.Todo {
	t := todo.New(argString(args, "title"))
	if project := argString(args, "project"); project != "" {
		t.Project = project
	}
	for _, tag := range chatArgTags(args) {
		t.AddTag(tag)
	}
	if p, ok := chatArgPriority(args); ok {
		t.Priority = p
	} else {
		t.Priority = todo.PriorityMedium
	}
	if due := argString(args, "due"); due != "" {
		if d, err := parseDueDate(due); err == nil {
			t.DueDate = d
		}
	}
	return t
}

// applyEditTaskArgs applies an edit_task call's arguments onto an existing
// task in place. Every field is optional — chatSystemPrompt tells the model
// to omit whatever should stay the same, so an absent argument here leaves
// that field untouched rather than clearing it.
func applyEditTaskArgs(t *todo.Todo, args map[string]any) {
	if title := argString(args, "title"); title != "" {
		t.Title = title
	}
	if project := argString(args, "project"); project != "" {
		t.SetProject(project)
	}
	for _, tag := range chatArgTags(args) {
		t.AddTag(tag)
	}
	if p, ok := chatArgPriority(args); ok {
		t.SetPriority(p)
	}
	if due := argString(args, "due"); due != "" {
		if d, err := parseDueDate(due); err == nil {
			t.SetDueDate(d)
		}
	}
}
