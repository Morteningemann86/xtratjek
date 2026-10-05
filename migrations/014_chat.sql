-- Migration 014: the Chat tab's persisted conversation.
--
-- Deliberately just role + content: the tool-calling scaffolding a reply is
-- computed through (tool calls, their results) never persists — chatops.go
-- rebuilds it fresh from this plain-text history on every new message, so
-- there is nothing provider-specific to store here.
CREATE TABLE chat_messages (
    id         TEXT PRIMARY KEY,
    role       TEXT NOT NULL,
    content    TEXT NOT NULL,
    created_at TEXT NOT NULL
);
