package main

import (
	"database/sql"
	"time"
)

// storage_chat.go is the SQLite backend for the Chat tab's conversation
// (migration 014) — one continuous, plain-text history. Writes are one
// message at a time and infrequent (a human typing, an AI reply landing),
// so like storage_meetings.go this writes straight through rather than
// batching through Store/Repository.

type chatMessage struct {
	ID        string
	Role      string // "user" | "assistant"
	Content   string
	CreatedAt time.Time
}

// saveChatMessage inserts msg. Chat messages are never edited once sent, so
// unlike saveMeeting this is an INSERT, not an upsert.
func saveChatMessage(msg chatMessage) error {
	if err := openStore(); err != nil {
		return err
	}
	return saveChatMessageIn(db, msg)
}

func saveChatMessageIn(h *sql.DB, msg chatMessage) error {
	_, err := h.Exec(`INSERT INTO chat_messages (id, role, content, created_at) VALUES (?, ?, ?, ?)`,
		msg.ID, msg.Role, msg.Content, fmtTime(msg.CreatedAt))
	return err
}

// clearChatMessages deletes the whole conversation (esc on the Chat tab).
func clearChatMessages() error {
	if err := openStore(); err != nil {
		return err
	}
	return clearChatMessagesIn(db)
}

func clearChatMessagesIn(h *sql.DB) error {
	_, err := h.Exec(`DELETE FROM chat_messages`)
	return err
}

// loadChatMessages returns the whole conversation, oldest first.
func loadChatMessages() ([]chatMessage, error) {
	if err := openStore(); err != nil {
		return nil, err
	}
	return loadChatMessagesIn(db)
}

func loadChatMessagesIn(h querier) ([]chatMessage, error) {
	rows, err := h.Query(`SELECT id, role, content, created_at FROM chat_messages ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []chatMessage
	for rows.Next() {
		var msg chatMessage
		var createdAt string
		if err := rows.Scan(&msg.ID, &msg.Role, &msg.Content, &createdAt); err != nil {
			return nil, err
		}
		msg.CreatedAt = parseTime(createdAt)
		out = append(out, msg)
	}
	return out, rows.Err()
}
