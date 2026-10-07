package main

import (
	"strings"
	"testing"

	"github.com/Morteningemann86/xtratjek/aiprovider"
	"github.com/Morteningemann86/xtratjek/todo"
)

// switchToChat drives the real "9" keypress rather than poking m.tab/m.mode
// directly, so these tests also exercise switchTab's mode wiring (model.go's
// modeChatInput doc comment) instead of assuming it.
// It also sets an API key, since enter refuses to send without one.
func switchToChat(t *testing.T, m model) model {
	t.Helper()
	m.aiKeys.Anthropic = "test-key"
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

func TestScriptChatEscResetsTheConversation(t *testing.T) {
	m := modelWithTasks(t)
	m = switchToChat(t, m)
	m = script(t, m, "hi", "enter")
	next, _ := m.Update(chatReplyMsg{text: "Hello!"})
	m = next.(model)
	m = sendKey(t, m, "half-typed")

	m = sendKey(t, m, "esc")
	if !m.chatConfirmReset || len(m.chatMessages) != 2 {
		t.Fatalf("esc should ask before clearing: confirm=%v messages=%d", m.chatConfirmReset, len(m.chatMessages))
	}
	m = sendKey(t, m, "y")
	if len(m.chatMessages) != 0 || m.chatInput.Value() != "" {
		t.Fatalf("after esc: messages=%+v input=%q, want both empty", m.chatMessages, m.chatInput.Value())
	}
	if m.tab != tabChat || m.mode != modeChatInput {
		t.Fatalf("esc left the chat: tab=%v mode=%v", m.tab, m.mode)
	}
	saved, err := loadChatMessages()
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 0 {
		t.Fatalf("loadChatMessages() after esc = %+v, want none", saved)
	}
}

func TestScriptChatEscClearsAPendingAction(t *testing.T) {
	m := modelWithTasks(t)
	m = switchToChat(t, m)
	m = script(t, m, "finish it", "enter")
	call := aiprovider.ToolCall{ID: "c", Name: toolCompleteTask, Arguments: map[string]any{"id": "x"}}
	next, _ := m.Update(chatReplyMsg{pendingAction: &call})
	m = next.(model)

	m = script(t, m, "esc", "y")
	if m.chatPendingAction != nil {
		t.Fatal("esc should drop the pending action")
	}
	m = sendKey(t, m, "y")
	if m.chatInput.Value() != "y" {
		t.Fatalf(`after reset "y" should be typed, got input %q`, m.chatInput.Value())
	}
}

func TestScriptChatReplyFromBeforeAResetIsDropped(t *testing.T) {
	m := modelWithTasks(t)
	m = switchToChat(t, m)
	m = script(t, m, "hi", "enter")
	stale := m.chatEpoch
	m = script(t, m, "esc", "y")

	next, _ := m.Update(chatReplyMsg{text: "late answer", epoch: stale})
	m = next.(model)
	if len(m.chatMessages) != 0 {
		t.Fatalf("a reply from before the reset landed: %+v", m.chatMessages)
	}
}

func TestScriptChatWithoutAKeyPointsAtSettings(t *testing.T) {
	m := modelWithTasks(t)
	m = switchToChat(t, m)
	m.aiKeys = aiprovider.Keys{}
	m.aiProvider = aiprovider.ProviderMistral

	view := m.renderChatMessages(80)
	if !strings.Contains(view, "Mistral") || !strings.Contains(view, "Settings") {
		t.Fatalf("chat pane without a key = %q, want it to name the provider and Settings", view)
	}

	m = script(t, m, "hi", "enter")
	if len(m.chatMessages) != 0 || m.chatLoading {
		t.Fatalf("enter without a key sent anyway: messages=%+v loading=%v", m.chatMessages, m.chatLoading)
	}
	if m.chatInput.Value() != "hi" {
		t.Fatalf("typed text should be kept, got %q", m.chatInput.Value())
	}
	if !strings.Contains(m.err, "Settings") {
		t.Fatalf("m.err = %q, want it to point at Settings", m.err)
	}

	m.aiKeys.Mistral = "k"
	if strings.Contains(m.renderChatMessages(80), "Settings") {
		t.Fatal("notice should disappear once the key is set")
	}
}

func TestScriptChatResetConfirmCanBeDeclined(t *testing.T) {
	for _, decline := range []string{"n", "esc"} {
		m := modelWithTasks(t)
		m = switchToChat(t, m)
		m = script(t, m, "hi", "enter")
		next, _ := m.Update(chatReplyMsg{text: "Hello!"})
		m = next.(model)

		m = script(t, m, "esc", "x", decline)
		if m.chatConfirmReset {
			t.Fatalf("%q should close the reset prompt", decline)
		}
		if len(m.chatMessages) != 2 {
			t.Fatalf("%q kept %d messages, want 2", decline, len(m.chatMessages))
		}
		if m.chatInput.Value() != "" {
			t.Fatalf("keys pressed while the prompt was open were typed: %q", m.chatInput.Value())
		}
		saved, err := loadChatMessages()
		if err != nil {
			t.Fatal(err)
		}
		if len(saved) != 2 {
			t.Fatalf("%q: stored messages = %d, want 2", decline, len(saved))
		}
		if decline == "n" && !strings.Contains(m.renderChatMessages(80), "Hello!") {
			t.Fatal("conversation should still render after declining")
		}
	}
}

func TestScriptChatResetPromptShowsInThePane(t *testing.T) {
	m := modelWithTasks(t)
	m = switchToChat(t, m)
	m = script(t, m, "hi", "enter", "esc")
	if !strings.Contains(m.renderChatMessages(80), "y/n") {
		t.Fatalf("reset prompt not rendered: %q", m.renderChatMessages(80))
	}
}

func TestScriptChatEscOnAnEmptyChatDoesNotAsk(t *testing.T) {
	m := modelWithTasks(t)
	m = switchToChat(t, m)
	m = sendKey(t, m, "esc")
	if m.chatConfirmReset {
		t.Fatal("nothing to clear, so esc should not ask")
	}
}

func TestScriptChatTabDropsTheResetPrompt(t *testing.T) {
	m := modelWithTasks(t)
	m = switchToChat(t, m)
	m = script(t, m, "hi", "esc", "tab")
	if m.chatConfirmReset {
		t.Fatal("leaving the tab should drop the reset prompt")
	}
}
