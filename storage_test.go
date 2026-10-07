package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/rank"
	"github.com/Morteningemann86/xtratjek/todo"
)

// ── task file (de)serialization ───────────────────────────────────────────────

// TestDecodeTaskFileLegacy ensures pre-versioning files (a bare JSON array of
// todos) still load, so existing users don't lose data after the upgrade.
func TestDecodeTaskFileLegacy(t *testing.T) {
	legacy, err := json.Marshal([]todo.Todo{todo.New("alpha"), todo.New("beta")})
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeTaskFile(legacy)
	if err != nil {
		t.Fatalf("decodeTaskFile(legacy) error: %v", err)
	}
	if len(got) != 2 || got[0].Title != "Alpha" || got[1].Title != "Beta" {
		t.Fatalf("legacy decode = %+v, want Alpha/Beta (todo.New capitalizes first letter)", got)
	}
}

// TestTaskFileRoundTrip ensures the current writer produces the versioned
// envelope and that it decodes back to the same todos.
func TestTaskFileRoundTrip(t *testing.T) {
	in := []todo.Todo{todo.New("one"), todo.New("two")}
	data, err := marshalTodos(in)
	if err != nil {
		t.Fatal(err)
	}

	var tf taskFile
	if err := json.Unmarshal(data, &tf); err != nil {
		t.Fatalf("written file is not the envelope shape: %v", err)
	}
	if tf.Version != currentTaskFileVersion {
		t.Fatalf("version = %d, want %d", tf.Version, currentTaskFileVersion)
	}

	got, err := decodeTaskFile(data)
	if err != nil {
		t.Fatalf("decodeTaskFile(envelope) error: %v", err)
	}
	if len(got) != 2 || got[0].Title != "One" || got[1].Title != "Two" {
		t.Fatalf("round-trip = %+v, want One/Two (todo.New capitalizes first letter)", got)
	}
}

// ── parseDueDate ──────────────────────────────────────────────────────────────

func TestParseDueDate(t *testing.T) {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	tests := []struct {
		name    string
		input   string
		want    time.Time
		wantErr bool
	}{
		{"today", "today", today, false},
		{"tomorrow", "tomorrow", today.AddDate(0, 0, 1), false},
		{"yesterday", "yesterday", today.AddDate(0, 0, -1), false},
		{"next week", "next week", today.AddDate(0, 0, 7), false},
		{"next month", "next month", today.AddDate(0, 1, 0), false},
		{"relative +3d", "+3d", today.AddDate(0, 0, 3), false},
		{"relative +2w", "+2w", today.AddDate(0, 0, 14), false},
		{"relative +1m", "+1m", today.AddDate(0, 1, 0), false},
		{"relative +10d", "+10d", today.AddDate(0, 0, 10), false},
		{"dd-mm-yy", "15-06-25", time.Date(2025, 6, 15, 0, 0, 0, 0, time.Local), false},
		{"dd-mm-yyyy", "15-06-2025", time.Date(2025, 6, 15, 0, 0, 0, 0, time.Local), false},
		{"invalid", "not-a-date", time.Time{}, true},
		{"empty", "", time.Time{}, true},
		{"garbage", "xyz123", time.Time{}, true},
		{"partial relative", "+d", time.Time{}, true},
		{"relative zero", "+0d", time.Time{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDueDate(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseDueDate(%q) error = %v, wantErr %v",
					tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && !got.Equal(tt.want) {
				t.Errorf("parseDueDate(%q) = %v, want %v",
					tt.input, got, tt.want)
			}
		})
	}
}

func TestParseDueDateWeekdays(t *testing.T) {
	weekdays := []string{
		"monday", "tuesday", "wednesday", "thursday",
		"friday", "saturday", "sunday",
		"mon", "tue", "wed", "thu", "fri", "sat", "sun",
	}

	for _, day := range weekdays {
		t.Run(day, func(t *testing.T) {
			got, err := parseDueDate(day)
			if err != nil {
				t.Errorf("parseDueDate(%q) unexpected error: %v", day, err)
				return
			}
			if got.IsZero() {
				t.Errorf("parseDueDate(%q) returned zero time", day)
				return
			}
			// Should be in the future
			now := time.Now()
			today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
			if !got.After(today) {
				t.Errorf("parseDueDate(%q) = %v, expected future date", day, got)
			}
		})
	}
}

func TestParseDueDateNextWeekday(t *testing.T) {
	prefixed := []string{
		"next monday", "next tuesday", "next wednesday",
		"next thursday", "next friday", "next saturday", "next sunday",
	}

	for _, input := range prefixed {
		t.Run(input, func(t *testing.T) {
			got, err := parseDueDate(input)
			if err != nil {
				t.Errorf("parseDueDate(%q) unexpected error: %v", input, err)
				return
			}
			now := time.Now()
			today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
			if !got.After(today) {
				t.Errorf("parseDueDate(%q) = %v, expected future date", input, got)
			}
		})
	}
}

// ── parseWeekday ──────────────────────────────────────────────────────────────

func TestParseWeekday(t *testing.T) {
	tests := []struct {
		input string
		want  time.Weekday
		ok    bool
	}{
		{"monday", time.Monday, true},
		{"mon", time.Monday, true},
		{"tuesday", time.Tuesday, true},
		{"tue", time.Tuesday, true},
		{"wednesday", time.Wednesday, true},
		{"wed", time.Wednesday, true},
		{"thursday", time.Thursday, true},
		{"thu", time.Thursday, true},
		{"friday", time.Friday, true},
		{"fri", time.Friday, true},
		{"saturday", time.Saturday, true},
		{"sat", time.Saturday, true},
		{"sunday", time.Sunday, true},
		{"sun", time.Sunday, true},
		{"invalid", 0, false},
		{"", 0, false},
		{"mond", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, ok := parseWeekday(tt.input)
			if ok != tt.ok {
				t.Errorf("parseWeekday(%q) ok = %v, want %v", tt.input, ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Errorf("parseWeekday(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// ── nextWeekday ───────────────────────────────────────────────────────────────

func TestNextWeekday(t *testing.T) {
	// Use a known Monday: 2025-01-06
	monday := time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		from   time.Time
		target time.Weekday
		want   time.Time
	}{
		{"monday to tuesday", monday, time.Tuesday, time.Date(2025, 1, 7, 0, 0, 0, 0, time.UTC)},
		{"monday to wednesday", monday, time.Wednesday, time.Date(2025, 1, 8, 0, 0, 0, 0, time.UTC)},
		{"monday to friday", monday, time.Friday, time.Date(2025, 1, 10, 0, 0, 0, 0, time.UTC)},
		{"monday to sunday", monday, time.Sunday, time.Date(2025, 1, 12, 0, 0, 0, 0, time.UTC)},
		{"monday to next monday", monday, time.Monday, time.Date(2025, 1, 13, 0, 0, 0, 0, time.UTC)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nextWeekday(tt.from, tt.target)
			if !got.Equal(tt.want) {
				t.Errorf("nextWeekday(%v, %v) = %v, want %v",
					tt.from.Format("Mon 02-01"), tt.target, got.Format("Mon 02-01"), tt.want.Format("Mon 02-01"))
			}
		})
	}
}

// ── parsePositiveInt ──────────────────────────────────────────────────────────

func TestParsePositiveInt(t *testing.T) {
	tests := []struct {
		input string
		want  int
		ok    bool
	}{
		{"0", 0, true},
		{"1", 1, true},
		{"42", 42, true},
		{"123", 123, true},
		{"999", 999, true},
		{"", 0, false},
		{"abc", 0, false},
		{"-1", 0, false},
		{"3.5", 0, false},
		{"12a", 0, false},
		{"a12", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, ok := parsePositiveInt(tt.input)
			if ok != tt.ok {
				t.Errorf("parsePositiveInt(%q) ok = %v, want %v", tt.input, ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Errorf("parsePositiveInt(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

// ── sortTodosByMode ───────────────────────────────────────────────────────────

func TestSortTodosByMode(t *testing.T) {
	now := time.Now()
	todos := []todo.Todo{
		{ID: "a", Title: "No date low", Priority: todo.PriorityLow, CreatedAt: now.Add(-3 * time.Hour)},
		{ID: "b", Title: "Tomorrow high", Priority: todo.PriorityHigh, DueDate: now.AddDate(0, 0, 1), CreatedAt: now.Add(-2 * time.Hour)},
		{ID: "c", Title: "Today medium", Priority: todo.PriorityMedium, DueDate: now, CreatedAt: now.Add(-1 * time.Hour)},
	}

	t.Run("by due date", func(t *testing.T) {
		cp := make([]todo.Todo, len(todos))
		copy(cp, todos)
		sortTodosByMode(cp, taskSortDueDate, rank.Default().ScoreNow())
		if cp[0].ID != "c" {
			t.Errorf("first should be 'c' (today), got %s", cp[0].ID)
		}
		if cp[1].ID != "b" {
			t.Errorf("second should be 'b' (tomorrow), got %s", cp[1].ID)
		}
		if cp[2].ID != "a" {
			t.Errorf("third should be 'a' (no date), got %s", cp[2].ID)
		}
	})

	t.Run("by sequence puts highest-scoring first", func(t *testing.T) {
		// 'b' is high-priority AND due tomorrow → highest score.
		// 'c' is medium AND due today → second.
		// 'a' is low with no date → lowest (just age).
		cp := make([]todo.Todo, len(todos))
		copy(cp, todos)
		sortTodosByMode(cp, taskSortSequence, rank.Default().ScoreNow())
		if cp[0].ID != "b" {
			t.Errorf("first should be 'b' (high+tomorrow), got %s", cp[0].ID)
		}
		if cp[2].ID != "a" {
			t.Errorf("last should be 'a' (low, no date), got %s", cp[2].ID)
		}
	})

	t.Run("empty slice", func(t *testing.T) {
		var empty []todo.Todo
		sortTodosByMode(empty, taskSortDueDate, rank.Default().ScoreNow()) // should not panic
	})

	t.Run("single item", func(t *testing.T) {
		single := []todo.Todo{{ID: "x"}}
		sortTodosByMode(single, taskSortDueDate, rank.Default().ScoreNow()) // should not panic
	})
}

// TestSequenceTieBreakChain pins the documented tie-break chain on the
// sequence sort: score → due proximity → size → CreatedAt → ID. Each subtest
// constructs a genuine score tie and checks the next key decides.
func TestSequenceTieBreakChain(t *testing.T) {
	created := time.Now().Add(-24 * time.Hour)

	t.Run("due proximity: sooner due first, no due last", func(t *testing.T) {
		// Dues beyond 7 days score urgency 0, so all three tie on score and
		// only the due tie-break separates them.
		todos := []todo.Todo{
			{ID: "none", Priority: todo.PriorityMedium, Size: todo.SizeMedium, CreatedAt: created},
			{ID: "far", Priority: todo.PriorityMedium, Size: todo.SizeMedium, CreatedAt: created, DueDate: time.Now().AddDate(0, 0, 20)},
			{ID: "near", Priority: todo.PriorityMedium, Size: todo.SizeMedium, CreatedAt: created, DueDate: time.Now().AddDate(0, 0, 10)},
		}
		rank.SortValues(todos, nil, nil, rank.Default().ScoreNow())
		if got := []string{todos[0].ID, todos[1].ID, todos[2].ID}; got[0] != "near" || got[1] != "far" || got[2] != "none" {
			t.Fatalf("want near/far/none, got %v", got)
		}
	})

	t.Run("size: quick win first when rollup ties the scores", func(t *testing.T) {
		// Two blockers boosted to the same inherited score — the case where
		// tasks of different sizes genuinely tie — sort small first.
		todos := []todo.Todo{
			{ID: "big", Priority: todo.PriorityMedium, Size: todo.SizeLarge, CreatedAt: created},
			{ID: "small", Priority: todo.PriorityMedium, Size: todo.SizeSmall, CreatedAt: created},
		}
		rollup := map[string]float64{"big": 50, "small": 50}
		rank.SortValues(todos, rollup, nil, rank.Default().ScoreNow())
		if todos[0].ID != "small" {
			t.Fatalf("want small first, got %s", todos[0].ID)
		}
	})

	t.Run("created: burst entry order is kept", func(t *testing.T) {
		// Aging off so the differing CreatedAt doesn't leak into the score —
		// the tie must be broken by the CreatedAt key itself.
		agingOff := rank.Ranker{Biases: rank.Biases{Aging: false}}.ScoreNow()
		todos := []todo.Todo{
			{ID: "later", Priority: todo.PriorityMedium, Size: todo.SizeMedium, CreatedAt: created.Add(time.Minute)},
			{ID: "earlier", Priority: todo.PriorityMedium, Size: todo.SizeMedium, CreatedAt: created},
		}
		rank.SortValues(todos, nil, nil, agingOff)
		if todos[0].ID != "earlier" {
			t.Fatalf("want earlier first, got %s", todos[0].ID)
		}
	})

	t.Run("id: absolute backstop", func(t *testing.T) {
		todos := []todo.Todo{
			{ID: "b", Priority: todo.PriorityMedium, Size: todo.SizeMedium, CreatedAt: created},
			{ID: "a", Priority: todo.PriorityMedium, Size: todo.SizeMedium, CreatedAt: created},
		}
		rank.SortValues(todos, nil, nil, rank.Default().ScoreNow())
		if todos[0].ID != "a" {
			t.Fatalf("want a first, got %s", todos[0].ID)
		}
	})
}

// A typed date is a calendar day where the user is, not in UTC: read as UTC
// midnight, it lands on the previous evening anywhere west of Greenwich.
func TestParseDueDateTypedDateIsLocal(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, ny)
	for _, in := range []string{"05-10-26", "05-10-2026"} {
		got, err := parseDueDateAt(in, now)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if want := time.Date(2026, 10, 5, 0, 0, 0, 0, ny); !got.Equal(want) {
			t.Errorf("%q = %v, want %v", in, got, want)
		}
	}
}
