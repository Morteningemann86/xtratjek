package main

import (
	"strings"
	"testing"

	"github.com/Iliorn/tjek/aiprovider"
	"github.com/Iliorn/tjek/todo"
)

// switchToChat drives the real "9" keypress rather than poking m.tab/m.mode
// directly, so these tests also exercise switchTab's mode wiring (model.go's
// modeChatInput doc comment) instead of assuming it.
func switchToChat(t *testing.T, m model) model {
	t.Helper()
	m = sendKey(t, m, "9")
	if m.tab != tabChat || m.mode != modeChatInput {
		t.Fatalf(`after "9": tab=%v mode=%v, want tabChat/modeChatInput`, m.tab, m.mode)
	}
	return m
}

// lastChatMessage is the most recent message in the conversation, or fails
// the test if there isn't one.
func lastChatMessage(t *testing.T, m model) chatMessage {
	t.Helper()
	if len(m.chatMessages) == 0 {
		t.Fatal("chatMessages is empty")
	}
	return m.chatMessages[len(m.chatMessages)-1]
}

func TestScriptChatDigitsAndLettersAreTypedNotShortcuts(t *testing.T) {
	m := modelWithTasks(t)
	m = switchToChat(t, m)

	// "q3" would quit and jump to Tags everywhere else in the app; here it
	// must just be text, since the textarea owns every keystroke the whole
	// time the tab is open (model.go's modeChatInput doc comment).
	m = sendKey(t, m, "q3 tasks")
	if m.chatInput.Value() != "q3 tasks" {
		t.Fatalf("chatInput.Value() = %q, want %q", m.chatInput.Value(), "q3 tasks")
	}
	if m.tab != tabChat || m.mode != modeChatInput {
		t.Fatalf("typing shortcut-shaped text changed tab/mode: tab=%v mode=%v", m.tab, m.mode)
	}
}

func TestScriptChatYAndNAreTypedWithoutAPendingAction(t *testing.T) {
	m := modelWithTasks(t)
	m = switchToChat(t, m)

	m = sendKey(t, m, "y")
	m = sendKey(t, m, "n")
	if m.chatInput.Value() != "yn" {
		t.Fatalf(`chatInput.Value() = %q, want "yn" (y/n only confirm a pending action)`, m.chatInput.Value())
	}
}

func TestScriptChatTabKeySwitchesTabs(t *testing.T) {
	m := modelWithTasks(t)
	m = switchToChat(t, m)

	m = sendKey(t, m, "tab")
	if m.tab == tabChat {
		t.Fatal(`"tab" from Chat did not switch away`)
	}
	if m.mode != modeNormal {
		t.Fatalf("after leaving Chat: mode = %v, want modeNormal", m.mode)
	}
}

func TestScriptChatSendMessagePersistsAndStartsAReply(t *testing.T) {
	m := modelWithTasks(t)
	m = switchToChat(t, m)

	m = sendKey(t, m, "what's open in Work?")
	m = sendKey(t, m, "enter")

	if len(m.chatMessages) != 1 || m.chatMessages[0].Role != "user" || m.chatMessages[0].Content != "what's open in Work?" {
		t.Fatalf("chatMessages = %+v", m.chatMessages)
	}
	if !m.chatLoading {
		t.Fatal("sending a message should set chatLoading")
	}
	if m.chatInput.Value() != "" {
		t.Fatalf("chatInput not cleared after send: %q", m.chatInput.Value())
	}

	// Actually persisted, not just held in memory.
	saved, err := loadChatMessages()
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 || saved[0].Content != "what's open in Work?" {
		t.Fatalf("loadChatMessages() = %+v", saved)
	}
}

func TestScriptChatBlankMessageDoesNothing(t *testing.T) {
	m := modelWithTasks(t)
	m = switchToChat(t, m)

	m = sendKey(t, m, "   ")
	m = sendKey(t, m, "enter")
	if len(m.chatMessages) != 0 {
		t.Fatalf("a blank message was sent: %+v", m.chatMessages)
	}
}

func TestScriptChatPlainReplyIsAppended(t *testing.T) {
	m := modelWithTasks(t)
	m = switchToChat(t, m)
	m = script(t, m, "hi", "enter")

	next, _ := m.Update(chatReplyMsg{text: "Hello! How can I help?"})
	m = next.(model)

	if m.chatLoading {
		t.Fatal("chatLoading should clear once a reply lands")
	}
	last := lastChatMessage(t, m)
	if last.Role != "assistant" || last.Content != "Hello! How can I help?" {
		t.Fatalf("last message = %+v", last)
	}
	saved, err := loadChatMessages()
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 2 {
		t.Fatalf("loadChatMessages() = %d messages, want 2", len(saved))
	}
}

func TestScriptChatErrorShowsAToastNotAMessage(t *testing.T) {
	m := modelWithTasks(t)
	m = switchToChat(t, m)
	m = script(t, m, "hi", "enter")

	next, _ := m.Update(chatReplyMsg{err: aiprovider.ErrNoAPIKey})
	m = next.(model)

	if m.chatLoading {
		t.Fatal("chatLoading should clear on error too")
	}
	if !strings.Contains(m.err, "Chat error") {
		t.Fatalf("m.err = %q, want it to mention the chat error", m.err)
	}
	if len(m.chatMessages) != 1 {
		t.Fatalf("an error reply should not append a chat message: %+v", m.chatMessages)
	}
}

func TestScriptChatCreateTaskConfirmed(t *testing.T) {
	m := modelWithTasks(t)
	m = switchToChat(t, m)
	m = script(t, m, "add a task to ship the report", "enter")
	tasksBefore := m.len()

	call := aiprovider.ToolCall{
		ID: "call_1", Name: toolCreateTask,
		Arguments: map[string]any{"title": "Ship the report", "project": "Work", "priority": "h"},
	}
	next, _ := m.Update(chatReplyMsg{pendingAction: &call})
	m = next.(model)

	if m.chatPendingAction == nil {
		t.Fatal("pendingAction was not set")
	}
	if !strings.Contains(m.chatPendingActionLabel, "Ship the report") {
		t.Fatalf("chatPendingActionLabel = %q, want it to mention the task title", m.chatPendingActionLabel)
	}

	m = sendKey(t, m, "y")
	if m.chatPendingAction != nil {
		t.Fatal("pendingAction should be cleared after confirming")
	}
	if m.len() != tasksBefore+1 {
		t.Fatalf("after confirming: store has %d tasks, want %d", m.len(), tasksBefore+1)
	}
	var created *todo.Todo
	for _, tk := range m.allTodos() {
		if tk.Title == "Ship the report" {
			created = tk
		}
	}
	if created == nil || created.Project != "Work" || created.Priority != todo.PriorityHigh {
		t.Fatalf("created task = %+v", created)
	}
	last := lastChatMessage(t, m)
	if last.Role != "assistant" || !strings.Contains(last.Content, "Ship the report") {
		t.Fatalf("confirmation message = %+v", last)
	}
}

func TestScriptChatCreateTaskDeclined(t *testing.T) {
	m := modelWithTasks(t)
	m = switchToChat(t, m)
	m = script(t, m, "add a task", "enter")
	tasksBefore := m.len()

	call := aiprovider.ToolCall{ID: "call_1", Name: toolCreateTask, Arguments: map[string]any{"title": "Ship the report"}}
	next, _ := m.Update(chatReplyMsg{pendingAction: &call})
	m = next.(model)

	m = sendKey(t, m, "n")
	if m.chatPendingAction != nil {
		t.Fatal("pendingAction should be cleared after declining")
	}
	if m.len() != tasksBefore {
		t.Fatalf("declining still created a task: store has %d tasks, want %d", m.len(), tasksBefore)
	}
	last := lastChatMessage(t, m)
	if last.Role != "assistant" || last.Content != tr("Okay, I didn't do that.") {
		t.Fatalf("decline message = %+v", last)
	}
}

func TestScriptChatCompleteTaskMissingIDDegradesGracefully(t *testing.T) {
	m := modelWithTasks(t)
	m = switchToChat(t, m)
	m = script(t, m, "mark the report done", "enter")

	call := aiprovider.ToolCall{ID: "call_1", Name: toolCompleteTask, Arguments: map[string]any{"id": "does-not-exist"}}
	next, _ := m.Update(chatReplyMsg{pendingAction: &call})
	m = next.(model)

	m = sendKey(t, m, "y")
	last := lastChatMessage(t, m)
	if !strings.Contains(last.Content, "Couldn't find") {
		t.Fatalf("result message = %+v, want a not-found message", last)
	}
}

func TestScriptChatCompleteTaskConfirmed(t *testing.T) {
	m := modelWithTasks(t, todo.New("Ship the report"))
	target := m.allTodos()[0]
	m = switchToChat(t, m)
	m = script(t, m, "mark it done", "enter")

	call := aiprovider.ToolCall{ID: "call_1", Name: toolCompleteTask, Arguments: map[string]any{"id": target.ID}}
	next, _ := m.Update(chatReplyMsg{pendingAction: &call})
	m = next.(model)
	m = sendKey(t, m, "y")

	if target.Status != todo.Done {
		t.Fatalf("task status = %v, want Done", target.Status)
	}
	last := lastChatMessage(t, m)
	if !strings.Contains(last.Content, "Ship the report") {
		t.Fatalf("confirmation message = %+v", last)
	}
}
