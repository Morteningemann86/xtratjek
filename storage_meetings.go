package main

import (
	"database/sql"
	"strings"
	"time"

	"github.com/Morteningemann86/xtratjek/meeting"
)

// storage_meetings.go is the SQLite backend for meetings and their AI
// suggestions (migration 012). Unlike todos, meetings have no undo stack and
// no sync fold yet (ARCHITECTURE.md's Repository/drainDirty machinery exists
// to serve those two things), so each call writes straight through on its own
// handle instead of batching through Store/Repository. Writes are infrequent
// (one meeting, one AI pass at a time) so there is no debounce to earn here.
//
// Each public function opens the shared singleton (openStore) and delegates
// to an *sql.DB-parameterized core, the same split loadTodosCore/
// saveNormalized use in storage_sqlite.go — so tests can drive the core
// against an isolated openStoreAt handle without touching the process-wide
// singleton.

// saveMeeting upserts m and stamps ModifiedAt first, so the caller never has
// to remember to call Touch themselves.
func saveMeeting(m *meeting.Meeting) error {
	if err := openStore(); err != nil {
		return err
	}
	return saveMeetingIn(db, m)
}

func saveMeetingIn(h *sql.DB, m *meeting.Meeting) error {
	m.Touch()
	_, err := h.Exec(`INSERT INTO meetings
		(id, title, date, attendees, audio_path, notes, transcript, summary, status, error_msg, created_at, modified_at, deleted, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			title=excluded.title, date=excluded.date, attendees=excluded.attendees,
			audio_path=excluded.audio_path, notes=excluded.notes, transcript=excluded.transcript, summary=excluded.summary,
			status=excluded.status, error_msg=excluded.error_msg, modified_at=excluded.modified_at,
			deleted=excluded.deleted, deleted_at=excluded.deleted_at`,
		m.ID, m.Title, fmtTime(m.Date), strings.Join(m.Attendees, ","), m.AudioPath,
		m.Notes, m.Transcript, m.Summary, string(m.Status), m.ErrorMsg,
		fmtTime(m.CreatedAt), fmtTime(m.ModifiedAt), boolToInt(m.Deleted), fmtTime(m.DeletedAt))
	return err
}

// loadMeetings returns every live meeting, most recent date first.
func loadMeetings() ([]meeting.Meeting, error) {
	if err := openStore(); err != nil {
		return nil, err
	}
	return loadMeetingsIn(db)
}

func loadMeetingsIn(h querier) ([]meeting.Meeting, error) {
	rows, err := h.Query(`SELECT id, title, date, attendees, audio_path, notes, transcript, summary,
		status, error_msg, created_at, modified_at
		FROM meetings WHERE deleted = 0 ORDER BY date DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []meeting.Meeting
	for rows.Next() {
		var m meeting.Meeting
		var status, date, attendees, createdAt, modifiedAt string
		if err := rows.Scan(&m.ID, &m.Title, &date, &attendees, &m.AudioPath, &m.Notes, &m.Transcript,
			&m.Summary, &status, &m.ErrorMsg, &createdAt, &modifiedAt); err != nil {
			return nil, err
		}
		m.Date = parseTime(date)
		m.SetAttendeesText(attendees)
		m.Status = meeting.Status(status)
		m.CreatedAt = parseTime(createdAt)
		m.ModifiedAt = parseTime(modifiedAt)
		out = append(out, m)
	}
	return out, rows.Err()
}

// deleteMeetingSoft tombstones a meeting (filtered out of loadMeetings) rather
// than hard-deleting it — cheap insurance against a stray keypress, matching
// the rest of the app's "deletes are tombstones" convention even though
// nothing here syncs the tombstone anywhere.
func deleteMeetingSoft(id string) error {
	if err := openStore(); err != nil {
		return err
	}
	return deleteMeetingSoftIn(db, id)
}

func deleteMeetingSoftIn(h *sql.DB, id string) error {
	_, err := h.Exec(`UPDATE meetings SET deleted = 1, deleted_at = ? WHERE id = ?`,
		fmtTime(time.Now()), id)
	return err
}

// replaceSuggestions deletes any existing suggestions for meetingID and
// inserts newSuggestions in their place. Called once, right after an AI
// extraction pass — re-running extraction on a meeting discards the previous
// (presumably worse, or superseded) candidate list rather than appending to
// it, so the review screen never shows two generations of guesses at once.
func replaceSuggestions(meetingID string, newSuggestions []meeting.Suggestion) error {
	if err := openStore(); err != nil {
		return err
	}
	return replaceSuggestionsIn(db, meetingID, newSuggestions)
}

func replaceSuggestionsIn(h *sql.DB, meetingID string, newSuggestions []meeting.Suggestion) error {
	tx, err := h.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM meeting_suggestions WHERE meeting_id = ?`, meetingID); err != nil {
		return err
	}
	ins, err := tx.Prepare(`INSERT INTO meeting_suggestions
		(id, meeting_id, title, suggested_project, suggested_tags, suggested_priority, suggested_due,
		 resolved, accepted, created_task_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer ins.Close()
	for _, s := range newSuggestions {
		if _, err := ins.Exec(s.ID, meetingID, s.Title, s.SuggestedProject,
			strings.Join(s.SuggestedTags, ","), s.SuggestedPriority, s.SuggestedDue,
			boolToInt(s.Resolved), boolToInt(s.Accepted), s.CreatedTaskID, fmtTime(s.CreatedAt)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// loadSuggestions returns every suggestion (resolved and unresolved) for a
// meeting, oldest first, so the review screen and any history view agree on
// order.
func loadSuggestions(meetingID string) ([]meeting.Suggestion, error) {
	if err := openStore(); err != nil {
		return nil, err
	}
	return loadSuggestionsIn(db, meetingID)
}

func loadSuggestionsIn(h querier, meetingID string) ([]meeting.Suggestion, error) {
	rows, err := h.Query(`SELECT id, meeting_id, title, suggested_project, suggested_tags,
		suggested_priority, suggested_due, resolved, accepted, created_task_id, created_at
		FROM meeting_suggestions WHERE meeting_id = ? ORDER BY created_at ASC`, meetingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []meeting.Suggestion
	for rows.Next() {
		var s meeting.Suggestion
		var tags, createdAt string
		var resolved, accepted int
		if err := rows.Scan(&s.ID, &s.MeetingID, &s.Title, &s.SuggestedProject, &tags,
			&s.SuggestedPriority, &s.SuggestedDue, &resolved, &accepted, &s.CreatedTaskID, &createdAt); err != nil {
			return nil, err
		}
		if tags != "" {
			s.SuggestedTags = strings.Split(tags, ",")
		}
		s.Resolved = resolved != 0
		s.Accepted = accepted != 0
		s.CreatedAt = parseTime(createdAt)
		out = append(out, s)
	}
	return out, rows.Err()
}

// resolveSuggestion marks one suggestion accepted or rejected, recording the
// task it created (empty on rejection).
func resolveSuggestion(id string, accepted bool, createdTaskID string) error {
	if err := openStore(); err != nil {
		return err
	}
	return resolveSuggestionIn(db, id, accepted, createdTaskID)
}

func resolveSuggestionIn(h *sql.DB, id string, accepted bool, createdTaskID string) error {
	_, err := h.Exec(`UPDATE meeting_suggestions SET resolved = 1, accepted = ?, created_task_id = ? WHERE id = ?`,
		boolToInt(accepted), createdTaskID, id)
	return err
}

// updateSuggestionEdits persists the review screen's edits to a still-
// unresolved suggestion (title/project/tags/priority/due), called as the user
// tabs off each field rather than per keystroke.
func updateSuggestionEdits(s meeting.Suggestion) error {
	if err := openStore(); err != nil {
		return err
	}
	return updateSuggestionEditsIn(db, s)
}

func updateSuggestionEditsIn(h *sql.DB, s meeting.Suggestion) error {
	_, err := h.Exec(`UPDATE meeting_suggestions SET
		title = ?, suggested_project = ?, suggested_tags = ?, suggested_priority = ?, suggested_due = ?
		WHERE id = ?`,
		s.Title, s.SuggestedProject, strings.Join(s.SuggestedTags, ","), s.SuggestedPriority, s.SuggestedDue, s.ID)
	return err
}
