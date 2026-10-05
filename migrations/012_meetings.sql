-- Migration 012: meetings.
--
-- Meeting notes (typed, pasted, or recorded and transcribed) and the AI-
-- proposed action items mined from them. Suggestions are kept once resolved
-- (not deleted) so a meeting's review history survives a restart mid-review.
-- Meetings are local-only: no tasksync fold has been written for them, so
-- unlike todos they do not leave the machine they were created on.
CREATE TABLE meetings (
    id          TEXT PRIMARY KEY,
    title       TEXT NOT NULL,
    date        TEXT NOT NULL,
    attendees   TEXT NOT NULL DEFAULT '', -- comma-separated; see meeting.Meeting.AttendeesText
    audio_path  TEXT NOT NULL DEFAULT '',
    transcript  TEXT NOT NULL DEFAULT '',
    summary     TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'draft',
    error_msg   TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    modified_at TEXT NOT NULL,
    deleted     INTEGER NOT NULL DEFAULT 0,
    deleted_at  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_meetings_live ON meetings(deleted, date);

CREATE TABLE meeting_suggestions (
    id                 TEXT PRIMARY KEY,
    meeting_id         TEXT NOT NULL REFERENCES meetings(id),
    title              TEXT NOT NULL,
    suggested_project  TEXT NOT NULL DEFAULT '',
    suggested_tags     TEXT NOT NULL DEFAULT '', -- comma-separated
    suggested_priority TEXT NOT NULL DEFAULT 'm',
    suggested_due      TEXT NOT NULL DEFAULT '',
    resolved           INTEGER NOT NULL DEFAULT 0,
    accepted           INTEGER NOT NULL DEFAULT 0,
    created_task_id    TEXT NOT NULL DEFAULT '',
    created_at         TEXT NOT NULL
);
CREATE INDEX idx_meeting_suggestions_meeting ON meeting_suggestions(meeting_id, resolved);

-- Traces an accepted suggestion's task back to the meeting it came from.
-- Empty-string default like every other optional todos column (parent_id,
-- project, ...): "no meeting" rather than NULL, consistent with the rest of
-- the table.
ALTER TABLE todos ADD COLUMN meeting_id TEXT NOT NULL DEFAULT '';
