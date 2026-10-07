package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Iliorn/tjek/aiprovider"

	"github.com/google/uuid"

	tea "github.com/charmbracelet/bubbletea"
)

// update_chat.go drives modeChatInput (model.go's doc comment on that mode
// explains why Chat gets a mode of its own rather than being dispatched by
// tab the way every other tab is) and handles chatReplyMsg, routed from
// dispatch's message-type switch (update.go) regardless of mode so a reply
// still lands correctly even if the user has since switched away from the
// tab. See chatops.go for the reply loop itself and chattools.go for the
// tool set the model it's calling can use.

// chatScrollStep is how many lines pgup/pgdn move chatScrollOffset —
// deliberately not a full page (unlike the task list's home/end/pgup/pgdn):
// a chat transcript is read a few lines at a time, not jumped through.
const chatScrollStep = 5

// updateChatInput is modeChatInput's entire key handler. tab/shift+tab and
// scrolling always work; everything else depends on whether a reset (esc)
// or a proposed action (handleChatReply) is awaiting y/n — while one is,
// only y/n (and esc, for the reset) do anything, so a stray keystroke can't be half-absorbed as the start of a
// new message while the question is still open. Otherwise enter sends and
// everything else is ordinary textarea editing, the same split
// updateEditMeetingText makes between its own reserved keys and the rest.
func (m model) updateChatInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "tab":
			m.chatConfirmReset = false
			m.switchTab(m.boardCfg.nextTab(m.tab, 1))
			return m, nil
		case "shift+tab":
			m.chatConfirmReset = false
			m.switchTab(m.boardCfg.nextTab(m.tab, -1))
			return m, nil
		case "pgup":
			m.chatScrollOffset += chatScrollStep
			return m, nil
		case "pgdown":
			m.chatScrollOffset = max(0, m.chatScrollOffset-chatScrollStep)
			return m, nil
		}
		if m.chatConfirmReset {
			switch key.String() {
			case "y":
				return m.resetChat()
			case "n", "esc":
				m.chatConfirmReset = false
			}
			return m, nil
		}
		if key.String() == "esc" {
			m.chatConfirmReset = m.chatHasSomethingToReset()
			return m, nil
		}
		if m.chatPendingAction != nil {
			switch key.String() {
			case "y":
				return m.confirmChatPendingAction()
			case "n":
				return m.declineChatPendingAction()
			}
			return m, nil
		}
		if key.String() == "enter" {
			return m.sendChatMessage()
		}
	}
	var cmd tea.Cmd
	m.chatInput, cmd = m.chatInput.Update(msg)
	return m, cmd
}

// sendChatMessage is enter in modeChatInput: append the typed message,
// persist it, and kick off chatReplyCmd against a fresh snapshot of
// whatever tjek currently holds. A blank message or one sent while a reply
// is already in flight does nothing — there is no queueing, since a second
// message before the first reply lands would race chatReplyCmd's own
// history against this one's.
func (m model) sendChatMessage() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.chatInput.Value())
	if text == "" || m.chatLoading {
		return m, nil
	}
	if notice := m.chatMissingKeyNotice(); notice != "" {
		// Keep the typed text: it can be sent once the key is added.
		m.flashError(notice)
		return m, clearErrAfter()
	}
	m.chatInput.Reset()
	m.chatScrollOffset = 0
	m.appendChatMessage("user", text)
	m.chatLoading = true
	snap := buildChatSnapshot(&m)
	history := chatHistoryToTurns(m.chatMessages)
	return m, chatReplyCmd(m.chatEpoch, history, snap, m.aiProvider, m.aiKeys)
}

// chatMissingKeyNotice says what to add in Settings when the configured
// provider has no API key, or "" when it has one. The pane shows it in
// place of the empty-conversation prompt and enter refuses to send, so a
// message is never stored that no reply can follow.
func (m model) chatMissingKeyNotice() string {
	if _, err := aiprovider.New(m.aiProvider, m.aiKeys); !errors.Is(err, aiprovider.ErrNoAPIKey) {
		return ""
	}
	return fmt.Sprintf(tr("No %s API key set. Add one in Settings (tab 7) under AI & Meetings to use Chat."),
		aiprovider.DisplayName(m.aiProvider))
}

// chatHasSomethingToReset reports whether esc has anything to clear, so it
// asks only when the answer matters.
func (m model) chatHasSomethingToReset() bool {
	return len(m.chatMessages) > 0 || m.chatPendingAction != nil || m.chatLoading ||
		m.chatInput.Value() != ""
}

// resetChat is "y" on esc's confirm: delete the conversation, a pending action and the
// typed text, and start over. Bumping chatEpoch orphans a reply still in
// flight (handleChatReply drops it).
func (m model) resetChat() (tea.Model, tea.Cmd) {
	m.chatConfirmReset = false
	m.chatEpoch++
	m.chatMessages = nil
	m.chatPendingAction = nil
	m.chatPendingActionLabel = ""
	m.chatLoading = false
	m.chatScrollOffset = 0
	m.chatInput.Reset()
	if err := clearChatMessages(); err != nil {
		m.flashError(fmt.Sprintf(tr("Error clearing chat: %v"), err))
	} else {
		m.flashInfo(tr("Chat cleared."))
	}
	return m, clearErrAfter()
}

// appendChatMessage adds msg to both the in-memory conversation and
// storage — a save failure is reported but not fatal to the in-memory
// side, the same "show it, keep going" handling resolveSuggestion's save
// gets in update_meetings.go.
func (m *model) appendChatMessage(role, content string) {
	msg := chatMessage{ID: uuid.New().String(), Role: role, Content: content, CreatedAt: time.Now()}
	m.chatMessages = append(m.chatMessages, msg)
	if err := saveChatMessage(msg); err != nil {
		m.flashError(fmt.Sprintf(tr("Error saving chat message: %v"), err))
	}
}

// handleChatReply lands chatReplyCmd's result: an error (shown as a toast,
// nothing added to the conversation), a pending action (describeChatAction
// computed once here — see chatPendingActionLabel's doc comment), or a
// plain reply (appended like any other message).
func (m model) handleChatReply(msg chatReplyMsg) (tea.Model, tea.Cmd) {
	if msg.epoch != m.chatEpoch {
		return m, nil
	}
	m.chatLoading = false
	switch {
	case msg.err != nil:
		m.flashError(fmt.Sprintf(tr("Chat error: %v"), msg.err))
		return m, clearErrAfter()
	case msg.pendingAction != nil:
		m.chatPendingAction = msg.pendingAction
		m.chatPendingActionLabel = describeChatAction(*msg.pendingAction, buildChatSnapshot(&m))
		return m, nil
	default:
		m.appendChatMessage("assistant", msg.text)
		return m, nil
	}
}

// confirmChatPendingAction is "y" on a pending action: apply it through the
// same mutation path a typed edit would use (applyChatAction) and report
// what happened as the assistant's next message — no second call to the
// provider, see chatops.go's package comment.
func (m model) confirmChatPendingAction() (tea.Model, tea.Cmd) {
	call := m.chatPendingAction
	if call == nil {
		return m, nil
	}
	m.chatPendingAction = nil
	m.chatPendingActionLabel = ""
	result := m.applyChatAction(*call)
	m.appendChatMessage("assistant", result)
	return m, nil
}

// declineChatPendingAction is "n" on a pending action: nothing is applied.
func (m model) declineChatPendingAction() (tea.Model, tea.Cmd) {
	m.chatPendingAction = nil
	m.chatPendingActionLabel = ""
	m.appendChatMessage("assistant", tr("Okay, I didn't do that."))
	return m, nil
}

// applyChatAction performs a confirmed create_task/complete_task/edit_task
// call and returns the plain-text result to show in chat. Each branch
// mirrors how the rest of the app already makes the same change —
// create_task follows acceptSuggestion's shape (meetingops.go/
// update_meetings.go: pushUndo, add, markModified), complete_task/
// edit_task follow m.get(id) + pushUndo + mutate + markModified, the same
// as any other in-place task edit. A task ID that no longer resolves
// (m.get returns nil — deleted since the model proposed acting on it) is
// reported back as plain text, never a crash.
func (m *model) applyChatAction(call aiprovider.ToolCall) string {
	switch call.Name {
	case toolCreateTask:
		t := buildCreateTaskFromArgs(call.Arguments)
		m.pushUndo(tr("create task via chat"), t.ID)
		m.add(t)
		m.markModified(t.ID)
		saveLastAddedID(t.ID)
		return fmt.Sprintf(tr("Created task %q."), t.Title)

	case toolCompleteTask:
		t := m.get(argString(call.Arguments, "id"))
		if t == nil {
			return tr("Couldn't find that task — it may have been deleted.")
		}
		m.pushUndo(tr("complete task via chat"), t.ID)
		t.Toggle()
		m.markModified(t.ID)
		return fmt.Sprintf(tr("Marked %q done."), t.Title)

	case toolEditTask:
		t := m.get(argString(call.Arguments, "id"))
		if t == nil {
			return tr("Couldn't find that task — it may have been deleted.")
		}
		m.pushUndo(tr("edit task via chat"), t.ID)
		applyEditTaskArgs(t, call.Arguments)
		m.markModified(t.ID)
		return fmt.Sprintf(tr("Updated %q."), t.Title)

	default:
		return tr("That wasn't something I know how to do.")
	}
}
