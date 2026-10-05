package main

import (
	"testing"
	"time"
)

func TestSaveAndLoadChatMessages(t *testing.T) {
	h := openTestStore(t)
	now := time.Now()
	msgs := []chatMessage{
		{ID: "1", Role: "user", Content: "what's open in Work?", CreatedAt: now},
		{ID: "2", Role: "assistant", Content: "You have one open task.", CreatedAt: now.Add(time.Second)},
	}
	for _, m := range msgs {
		if err := saveChatMessageIn(h, m); err != nil {
			t.Fatalf("saveChatMessageIn: %v", err)
		}
	}

	got, err := loadChatMessagesIn(h)
	if err != nil {
		t.Fatalf("loadChatMessagesIn: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("loadChatMessagesIn() = %d messages, want 2", len(got))
	}
	if got[0].ID != "1" || got[0].Role != "user" || got[0].Content != "what's open in Work?" {
		t.Fatalf("loaded[0] = %+v", got[0])
	}
	if got[1].ID != "2" || got[1].Role != "assistant" {
		t.Fatalf("loaded[1] = %+v", got[1])
	}
}

func TestLoadChatMessagesEmpty(t *testing.T) {
	h := openTestStore(t)
	got, err := loadChatMessagesIn(h)
	if err != nil {
		t.Fatalf("loadChatMessagesIn: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("loadChatMessagesIn() = %d messages, want 0", len(got))
	}
}
