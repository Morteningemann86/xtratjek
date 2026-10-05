// Package todo is tjek's framework-free domain layer: the Todo type and its
// mutations (toggle, tags, timers, subtasks, comments, time entries).
// It carries no Bubble Tea or rendering concerns.
package todo

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

// StampModified returns the timestamp to use for a mutation's new ModifiedAt.
// It returns max(time.Now(), prev+1ms), guaranteeing the stamp is strictly
// later than the record version being replaced. This matters when the local
// clock runs behind: without the clamp, an edit made on top of version X can
// carry an older ModifiedAt than X itself, causing the sync merge (last
// ModifiedAt wins) to silently discard the edit in favour of the stale sibling
// from another device. Callers that are creating a brand-new record with no
// previous version should pass the zero time; the max then collapses to
// time.Now() since any real wall-clock value exceeds zero+1ms.
func StampModified(prev time.Time) time.Time {
	return StampAt(time.Now(), prev)
}

// StampAt is StampModified with the "now" reading supplied by the caller: the
// stamp is max(at, prev+1ms). Use it when the event being stamped happened at
// a known moment that is not the moment of the call — a deletion recorded
// when the user pressed the key but written by a debounced save 300ms later,
// say. Ordering against prev is preserved either way.
func StampAt(at, prev time.Time) time.Time {
	if floor := prev.Add(time.Millisecond); floor.After(at) {
		return floor
	}
	return at
}

// CapitalizeTitle uppercases the first rune of s if it is a lowercase letter,
// leaving the rest of s untouched. Empty strings and titles that start with a
// non-letter (digit, emoji, punctuation) or are already uppercase are returned
// as-is.
func CapitalizeTitle(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	if !unicode.IsLetter(r) || !unicode.IsLower(r) {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
}

// ── Status ────────────────────────────────────────────────────────────────────

type Status int

const (
	Pending Status = iota
	Done
)

// ── Priority ──────────────────────────────────────────────────────────────────

type Priority int

const (
	PriorityLow Priority = iota
	PriorityMedium
	PriorityHigh
)

func (p Priority) String() string {
	switch p {
	case PriorityHigh:
		return "high"
	case PriorityMedium:
		return "medium"
	default:
		return "low"
	}
}

func (p Priority) Icon() string {
	switch p {
	case PriorityHigh:
		return "↑"
	case PriorityMedium:
		return "→"
	default:
		return "↓"
	}
}

// ── Size ──────────────────────────────────────────────────────────────────────

// Size is the user's coarse estimate of how much effort a task will take. It
// feeds the Momentum dimension of the sequencing score: Small tasks rank
// highest (the "small-task floor") so a 5-minute win can outrank a large
// project sitting at the same priority/deadline.
//
// SizeMedium is the zero value so existing JSON blobs and pre-migration SQLite
// rows (added with DEFAULT 0) deserialize as Medium — a neutral default that
// matches "no opinion".
type Size int

const (
	SizeMedium Size = iota
	SizeSmall
	SizeLarge
)

func (s Size) String() string {
	switch s {
	case SizeSmall:
		return "small"
	case SizeLarge:
		return "large"
	default:
		return "medium"
	}
}

// Letter is the one-character form used in compact UI columns.
func (s Size) Letter() string {
	switch s {
	case SizeSmall:
		return "S"
	case SizeLarge:
		return "L"
	default:
		return "M"
	}
}

// Rank orders sizes Small < Medium < Large for sorting — smaller first matches
// the quick-win reading everywhere sizes break a tie. The iota order of the
// constants is storage order, not this one.
func (s Size) Rank() int {
	switch s {
	case SizeSmall:
		return 0
	case SizeMedium:
		return 1
	default: // Large
		return 2
	}
}

// ── Comment ───────────────────────────────────────────────────────────────────

type Comment struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
	// ModifiedAt orders two live versions of the same record during the sync
	// merge (later edit wins; see mergeChildren). Zero on records written
	// before the field existed — the merge falls back to a hash tiebreak then.
	ModifiedAt time.Time `json:"modified_at,omitempty"`
	// DeletedAt tombstones the record for cross-device sync (see merge.go); the
	// zero value means live. Kept rather than removed so a deletion propagates.
	DeletedAt time.Time `json:"deleted_at,omitempty"`
}

// ── TimeEntry ─────────────────────────────────────────────────────────────────

type TimeEntry struct {
	ID         string    `json:"id"`
	StartedAt  time.Time `json:"started_at"`
	StoppedAt  time.Time `json:"stopped_at,omitempty"`
	ModifiedAt time.Time `json:"modified_at,omitempty"` // sync merge recency; see Comment.ModifiedAt
	DeletedAt  time.Time `json:"deleted_at,omitempty"`  // sync tombstone; see Comment.DeletedAt
	// LastSeen is the last moment a live tjek process confirmed this timer was
	// still running (heartbeat). A running entry whose LastSeen has gone stale is
	// treated as abandoned and recovered. Zero = never heartbeated.
	LastSeen time.Time `json:"last_seen,omitempty"`
}

func (te TimeEntry) Duration() time.Duration {
	if te.StoppedAt.IsZero() {
		return time.Since(te.StartedAt)
	}
	return te.StoppedAt.Sub(te.StartedAt)
}

func (te TimeEntry) IsRunning() bool {
	return te.StoppedAt.IsZero()
}

// ── Todo ──────────────────────────────────────────────────────────────────────

type Todo struct {
	ID           string      `json:"id"`
	Title        string      `json:"title"`
	Status       Status      `json:"status"`
	Priority     Priority    `json:"priority"`
	Size         Size        `json:"size,omitempty"`
	CreatedAt    time.Time   `json:"created_at"`
	ModifiedAt   time.Time   `json:"modified_at"`
	CompletedAt  time.Time   `json:"completed_at,omitempty"`
	StartDate    time.Time   `json:"start_date,omitempty"`
	DueDate      time.Time   `json:"due_date,omitempty"`
	Project      string      `json:"project,omitempty"`
	Tags         []string    `json:"tags,omitempty"`
	Dependencies []string    `json:"dependencies,omitempty"`
	Comments     []Comment   `json:"comments,omitempty"`
	TimeEntries  []TimeEntry `json:"time_entries,omitempty"`
	Notes        string      `json:"notes,omitempty"`
	ParentID     string      `json:"parent_id,omitempty"`
	Recurrence   string      `json:"recurrence,omitempty"`

	// MeetingID traces this task back to the meeting whose action-item review
	// created it ("" for every task not created that way). Set once, at
	// creation, by accepting a meeting.Suggestion; never edited afterward.
	MeetingID string `json:"meeting_id,omitempty"`

	// Stage is the kanban board column a pending top-level task sits in — one
	// of the user-configured stage names (settings.json "stages"). Empty means
	// the first configured stage, so existing tasks need no backfill and a
	// renamed stage strands its tasks visibly in the first column rather than
	// hiding them. Completion is NOT a stage: the board's final column is
	// Status==Done itself, so "done" never has two sources of truth. The todo
	// package stores the name verbatim; validation against the configured
	// list is the caller's concern (this package stays configuration-free).
	Stage string `json:"stage,omitempty"`

	// SeqRankAtDone records the 1-based position this task held in the
	// top-level sequence ranking at the moment a user completed it (0 = not
	// recorded: legacy completions, subtasks, auto-closed parents). Feeds the
	// "sequence hit rate" stat — how often what you finish is what the engine
	// had on top.
	SeqRankAtDone int `json:"seq_rank_done,omitempty"`

	// Tombstone fields for cross-device sync: a deleted task is retained as a
	// tombstone (Deleted=true, DeletedAt set) so the deletion propagates during
	// sync instead of reappearing from another device. Storage already keeps
	// soft-deleted rows; these surface that state on the struct and the wire.
	Deleted   bool      `json:"deleted,omitempty"`
	DeletedAt time.Time `json:"deleted_at,omitempty"`
}

func New(title string) Todo {
	now := time.Now()
	return Todo{
		ID:         uuid.New().String(),
		Title:      CapitalizeTitle(title),
		Status:     Pending,
		Priority:   PriorityMedium,
		CreatedAt:  now,
		ModifiedAt: now,
	}
}

func NewSubtask(title string, parentID string) Todo {
	t := New(title)
	t.ParentID = parentID
	t.Size = SizeSmall
	return t
}

// InheritContextFrom copies the parent's Project and DueDate into t, and its
// Tags when tags is true, and caps t's priority at the parent's. Callers use
// this when creating a subtask so it picks up the same context (project board,
// tag filters, deadline) as the parent without the user having to retype it.
// Tags are the caller's choice because a tag often describes one task rather
// than the work it belongs to. The priority cap is part of that context: a
// fresh subtask defaults to Medium, which would otherwise land a brand-new
// child above a parent the user parked at Low.
func (t *Todo) InheritContextFrom(parent *Todo, tags bool) {
	if parent == nil {
		return
	}
	if parent.Priority < t.Priority {
		t.Priority = parent.Priority
	}
	if parent.Project != "" {
		t.Project = parent.Project
	}
	if tags {
		for _, tag := range parent.Tags {
			t.AddTag(tag)
		}
	}
	if !parent.DueDate.IsZero() {
		t.DueDate = parent.DueDate
	}
}

func (t *Todo) Toggle() {
	if t.Status == Pending {
		t.Status = Done
		t.CompletedAt = time.Now()
	} else {
		t.Status = Pending
		t.CompletedAt = time.Time{}
		t.SeqRankAtDone = 0 // the rank is a completion-time reading; reopening voids it
	}
	t.ModifiedAt = StampModified(t.ModifiedAt)
}

// SetCompletedAt corrects when a done task was finished, for a task ticked off
// late. It leaves Status alone: reopening is Toggle's job.
func (t *Todo) SetCompletedAt(d time.Time) {
	t.CompletedAt = d
	t.ModifiedAt = StampModified(t.ModifiedAt)
}

func (t *Todo) SetDueDate(d time.Time) {
	t.DueDate = d
	t.ModifiedAt = StampModified(t.ModifiedAt)
}

// SetStartDate records when work on a task begins. StartDate holds a full
// timestamp (not just a date) so start→done cycle time is precise: starting
// "today" stamps the moment of entry, while a start date on any other day —
// which has no natural time of day — defaults to 09:00 local, a sensible start
// of the workday rather than midnight. The UI still accepts and shows a plain
// date, revealing the time only when one is present.
func (t *Todo) SetStartDate(d time.Time) {
	now := time.Now()
	if sameDay(d, now) {
		d = now
	} else {
		d = time.Date(d.Year(), d.Month(), d.Day(), 9, 0, 0, 0, d.Location())
	}
	t.StartDate = d
	t.ModifiedAt = StampModified(t.ModifiedAt)
}

// sameDay reports whether a and b fall on the same calendar day (each in its
// own location).
func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func (t *Todo) SetPriority(p Priority) {
	t.Priority = p
	t.ModifiedAt = StampModified(t.ModifiedAt)
}

func (t *Todo) SetSize(s Size) {
	t.Size = s
	t.ModifiedAt = StampModified(t.ModifiedAt)
}

func (t *Todo) SetProject(p string) {
	t.Project = p
	t.ModifiedAt = StampModified(t.ModifiedAt)
}

// SetStage moves the task to a board stage. The empty string is valid — it
// means "first configured stage" — so callers can reset a task without
// knowing the configured names.
func (t *Todo) SetStage(s string) {
	t.Stage = s
	t.ModifiedAt = StampModified(t.ModifiedAt)
}

// NormalizeTag canonicalizes a tag so that "#Deep Work", "deep-work ", and
// "DEEP   WORK" all collapse to the whitespace-free slug "deep-work". Tags
// are used as whitespace-delimited #tokens in quick-add/search syntax, so
// allowing spaces in storage would make the displayed token ambiguous.
// Returns "" for input that isn't a usable tag.
func NormalizeTag(tag string) string {
	tag = strings.TrimSpace(tag)
	tag = strings.TrimPrefix(tag, "#")
	return strings.ToLower(strings.Join(strings.Fields(tag), "-"))
}

func (t *Todo) AddTag(tag string) {
	tag = NormalizeTag(tag)
	if tag == "" {
		return
	}
	found := false
	changed := false
	tags := t.Tags[:0]
	for _, existing := range t.Tags {
		if NormalizeTag(existing) == tag {
			if found {
				changed = true // collapse a legacy alias duplicate
				continue
			}
			found = true
			tags = append(tags, tag)
			changed = changed || existing != tag
			continue
		}
		tags = append(tags, existing)
	}
	if found {
		t.Tags = tags
		if changed {
			t.ModifiedAt = StampModified(t.ModifiedAt)
		}
		return
	}
	t.Tags = append(tags, tag)
	t.ModifiedAt = StampModified(t.ModifiedAt)
}

func (t *Todo) RemoveTag(tag string) {
	tag = NormalizeTag(tag)
	tags := t.Tags[:0]
	for _, existing := range t.Tags {
		if NormalizeTag(existing) != tag {
			tags = append(tags, existing)
		}
	}
	if len(tags) == len(t.Tags) {
		return // tag wasn't present — don't bump ModifiedAt on a no-op
	}
	t.Tags = tags
	t.ModifiedAt = StampModified(t.ModifiedAt)
}

func (t *Todo) AddDependency(id string) {
	for _, dep := range t.Dependencies {
		if dep == id {
			return
		}
	}
	t.Dependencies = append(t.Dependencies, id)
	t.ModifiedAt = StampModified(t.ModifiedAt)
}

func (t *Todo) RemoveDependency(id string) {
	deps := t.Dependencies[:0]
	for _, dep := range t.Dependencies {
		if dep != id {
			deps = append(deps, dep)
		}
	}
	if len(deps) == len(t.Dependencies) {
		return // id wasn't a dependency — don't bump ModifiedAt on a no-op
	}
	t.Dependencies = deps
	t.ModifiedAt = StampModified(t.ModifiedAt)
}

func (t *Todo) AddComment(text string) {
	wall := time.Now()
	stamp := StampModified(t.ModifiedAt)
	t.Comments = append(t.Comments, Comment{
		ID:         uuid.New().String(),
		Text:       text,
		CreatedAt:  wall, // domain timestamp: when the comment was written
		ModifiedAt: stamp,
	})
	t.ModifiedAt = stamp
}

func (t *Todo) UpdateComment(index int, text string) {
	if index >= 0 && index < len(t.Comments) {
		t.Comments[index].Text = text
		t.Comments[index].ModifiedAt = StampModified(t.Comments[index].ModifiedAt)
		t.ModifiedAt = StampModified(t.ModifiedAt)
	}
}

func (t *Todo) DeleteComment(index int) {
	if index >= 0 && index < len(t.Comments) {
		t.Comments = append(t.Comments[:index], t.Comments[index+1:]...)
		t.ModifiedAt = StampModified(t.ModifiedAt)
	}
}

// ── Time tracking ─────────────────────────────────────────────────────────────

func (t *Todo) StartTimer() {
	t.StopTimer()
	wall := time.Now()
	stamp := StampModified(t.ModifiedAt)
	// The first timer start also marks when work began: if no start date was
	// set manually, backfill it to this moment so start→done cycle-time stats
	// capture the task, with the precise time of day (same rule as starting
	// "today" via SetStartDate). StartDate is a domain timestamp (wall-clock
	// moment), not a merge-ordering stamp, so it uses the real now rather than
	// the clamped value.
	if t.StartDate.IsZero() {
		t.StartDate = wall
	}
	t.TimeEntries = append(t.TimeEntries, TimeEntry{
		ID:         uuid.New().String(),
		StartedAt:  wall,
		ModifiedAt: stamp,
		// Born with a heartbeat: LastSeen starts defined so a save of this task
		// never writes an empty last_seen over a fresher DB-side heartbeat
		// before the first tick-driven stamp arrives.
		LastSeen: wall,
	})
	t.ModifiedAt = stamp
}

// AddTimeEntry appends a completed entry for [start, stop) and returns its
// generated ID. Used by the "manual time entry" flow when the user wants to
// log work that wasn't captured by the live timer.
func (t *Todo) AddTimeEntry(start, stop time.Time) string {
	id := uuid.New().String()
	now := StampModified(t.ModifiedAt)
	t.TimeEntries = append(t.TimeEntries, TimeEntry{
		ID:         id,
		StartedAt:  start,
		StoppedAt:  stop,
		ModifiedAt: now,
	})
	t.ModifiedAt = now
	return id
}

// StopTimer stops every running entry. Stamping the stopped entry's ModifiedAt
// matters for sync: another device still holds the *running* version of the
// same entry, and the newer stop must win that merge or the timer resurrects.
func (t *Todo) StopTimer() {
	wall := time.Now()
	stamp := StampModified(t.ModifiedAt)
	for i := range t.TimeEntries {
		if t.TimeEntries[i].IsRunning() {
			t.TimeEntries[i].StoppedAt = wall   // domain: when the clock stopped
			t.TimeEntries[i].ModifiedAt = stamp // merge ordering: must beat running copy
		}
	}
	t.ModifiedAt = stamp
}

func (t *Todo) IsTimerRunning() bool {
	for i := range t.TimeEntries {
		if t.TimeEntries[i].IsRunning() {
			return true
		}
	}
	return false
}

func (t *Todo) RunningEntry() *TimeEntry {
	for i := range t.TimeEntries {
		if t.TimeEntries[i].IsRunning() {
			return &t.TimeEntries[i]
		}
	}
	return nil
}

func (t *Todo) TotalTimeSpent() time.Duration {
	var total time.Duration
	for _, entry := range t.TimeEntries {
		total += entry.Duration()
	}
	return total
}

func (t *Todo) DeleteTimeEntry(index int) {
	if index >= 0 && index < len(t.TimeEntries) {
		t.TimeEntries = append(t.TimeEntries[:index], t.TimeEntries[index+1:]...)
		t.ModifiedAt = StampModified(t.ModifiedAt)
	}
}

// Subtasks: the parent→child link is stored only on the child as ParentID (the
// single source of truth). A parent's subtask list is derived from it — see
// model.subtaskIDs — rather than duplicated on the parent.

// ── Notes ─────────────────────────────────────────────────────────────────────

func (t *Todo) SetNotes(notes string) {
	t.Notes = notes
	t.ModifiedAt = StampModified(t.ModifiedAt)
}

// ── Recurrence ────────────────────────────────────────────────────────────────
//
// A task with a non-empty Recurrence respawns when marked Done: a fresh
// pending copy with a new ID is added to the store, and the original keeps
// its completion history. ParseRecurrence is the input validator; canonical
// rules are: "daily", "weekly", "monthly", "yearly", "weekdays", and
// "every:Nd|Nw|Nm|Ny" (N ≥ 1). NextRecurrenceFrom computes the next instance's
// date given the rule and a base time (typically the previous DueDate, or
// CompletedAt when no due date is set).

func (t *Todo) IsRecurring() bool { return t.Recurrence != "" }

func (t *Todo) SetRecurrence(rule string) {
	t.Recurrence = rule
	t.ModifiedAt = StampModified(t.ModifiedAt)
}

func (t *Todo) ClearRecurrence() {
	t.Recurrence = ""
	t.ModifiedAt = StampModified(t.ModifiedAt)
}

// ParseRecurrence canonicalizes a user-supplied recurrence string. Returns the
// canonical form and true if recognized. The empty string parses as ("", true)
// — a way to clear an existing rule via the same path.
func ParseRecurrence(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", true
	}
	switch s {
	case "daily", "day":
		return "daily", true
	case "weekly", "week":
		return "weekly", true
	case "monthly", "month":
		return "monthly", true
	case "yearly", "year", "annual", "annually":
		return "yearly", true
	case "weekdays", "weekday":
		return "weekdays", true
	}
	// "every:Nd|Nw|Nm|Ny" and the shorthand "Nd|Nw|Nm|Ny".
	spec := strings.TrimPrefix(s, "every:")
	if len(spec) >= 2 {
		unit := spec[len(spec)-1]
		numStr := spec[:len(spec)-1]
		n, ok := parsePositiveInt(numStr)
		if ok && n >= 1 {
			switch unit {
			case 'd', 'w', 'm', 'y':
				if n == 1 {
					switch unit {
					case 'd':
						return "daily", true
					case 'w':
						return "weekly", true
					case 'm':
						return "monthly", true
					case 'y':
						return "yearly", true
					}
				}
				return fmt.Sprintf("every:%d%c", n, unit), true
			}
		}
	}
	return "", false
}

// NextRecurrenceFrom returns the next instance date for rule, computed from
// base. Returns (zero, false) when rule is invalid or empty. The result keeps
// the wall-clock time of base (so "daily" with a base at 09:00 lands on the
// next day at 09:00). "weekdays" advances to the next Mon–Fri; if base is
// itself a weekday, it advances by one weekday.
func NextRecurrenceFrom(rule string, base time.Time) (time.Time, bool) {
	if rule == "" || base.IsZero() {
		return time.Time{}, false
	}
	switch rule {
	case "daily":
		return base.AddDate(0, 0, 1), true
	case "weekly":
		return base.AddDate(0, 0, 7), true
	case "monthly":
		return base.AddDate(0, 1, 0), true
	case "yearly":
		return base.AddDate(1, 0, 0), true
	case "weekdays":
		next := base.AddDate(0, 0, 1)
		for {
			wd := next.Weekday()
			if wd != time.Saturday && wd != time.Sunday {
				return next, true
			}
			next = next.AddDate(0, 0, 1)
		}
	}
	if strings.HasPrefix(rule, "every:") {
		spec := strings.TrimPrefix(rule, "every:")
		if len(spec) >= 2 {
			unit := spec[len(spec)-1]
			n, ok := parsePositiveInt(spec[:len(spec)-1])
			if ok && n >= 1 {
				switch unit {
				case 'd':
					return base.AddDate(0, 0, n), true
				case 'w':
					return base.AddDate(0, 0, n*7), true
				case 'm':
					return base.AddDate(0, n, 0), true
				case 'y':
					return base.AddDate(n, 0, 0), true
				}
			}
		}
	}
	return time.Time{}, false
}

func parsePositiveInt(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}

// ── Query helpers ─────────────────────────────────────────────────────────────

func (t *Todo) IsOverdue() bool {
	return t.IsOverdueAt(time.Now())
}

// IsOverdueAt is the clock-injectable form. Callers that need deterministic
// behavior (stats buckets, tests, anything pinned to a specific moment)
// should pass `now` explicitly so the result doesn't drift with the wall
// clock between invocations.
func (t *Todo) IsOverdueAt(now time.Time) bool {
	if t.Status == Done {
		return false
	}
	if t.DueDate.IsZero() {
		return false
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return t.DueDate.Before(today)
}

func (t *Todo) HasOverdueDependencyFast(overdueSet map[string]bool) bool {
	for _, depID := range t.Dependencies {
		if overdueSet[depID] {
			return true
		}
	}
	return false
}

func (t *Todo) IsDueToday() bool {
	if t.DueDate.IsZero() || t.Status == Done {
		return false
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	tomorrow := today.AddDate(0, 0, 1)
	return !t.DueDate.Before(today) && t.DueDate.Before(tomorrow)
}

func (t *Todo) IsDueSoon(days int) bool {
	if t.DueDate.IsZero() || t.Status == Done {
		return false
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	deadline := today.AddDate(0, 0, days)
	return !t.DueDate.Before(today) && t.DueDate.Before(deadline)
}

func (t *Todo) IsTopLevel() bool {
	return t.ParentID == ""
}
