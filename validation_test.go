package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Morteningemann86/xtratjek/todo"
)

// captureValidationWarnings swaps the package-level validationWarn for the
// duration of the test so each clamping records into the returned buffer
// instead of polluting test stderr. Returns the buffer + a restore func.
func captureValidationWarnings(t *testing.T) (*strings.Builder, func()) {
	t.Helper()
	var buf strings.Builder
	prev := validationWarn
	validationWarn = func(format string, args ...any) {
		fmt.Fprintf(&buf, format, args...)
	}
	return &buf, func() { validationWarn = prev }
}

func TestSafeEnumsClampOutOfRange(t *testing.T) {
	buf, restore := captureValidationWarnings(t)
	defer restore()

	if got := safeStatus(99, "tid-status"); got != todo.Pending {
		t.Errorf("safeStatus(99) = %v, want Pending", got)
	}
	if got := safePriority(42, "tid-prio"); got != todo.PriorityMedium {
		t.Errorf("safePriority(42) = %v, want PriorityMedium", got)
	}
	if got := safeSize(-1, "tid-size"); got != todo.SizeMedium {
		t.Errorf("safeSize(-1) = %v, want SizeMedium", got)
	}

	out := buf.String()
	for _, want := range []string{"tid-status", "tid-prio", "tid-size", "clamped"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected warning to mention %q, got:\n%s", want, out)
		}
	}
}

func TestSafeEnumsAcceptInRangeValues(t *testing.T) {
	buf, restore := captureValidationWarnings(t)
	defer restore()

	cases := []struct {
		name string
		got  any
		want any
	}{
		{"status Pending", safeStatus(0, "x"), todo.Pending},
		{"status Done", safeStatus(1, "x"), todo.Done},
		{"priority Low", safePriority(0, "x"), todo.PriorityLow},
		{"priority High", safePriority(2, "x"), todo.PriorityHigh},
		{"size Medium", safeSize(0, "x"), todo.SizeMedium},
		{"size Small", safeSize(1, "x"), todo.SizeSmall},
		{"size Large", safeSize(2, "x"), todo.SizeLarge},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %v want %v", c.name, c.got, c.want)
		}
	}
	if buf.Len() > 0 {
		t.Errorf("expected zero warnings on valid values, got:\n%s", buf.String())
	}
}

// TestLoadTodosClampsCorruptRow inserts a row directly via SQL with out-of-
// range scalars (mimicking a corrupt migration or manual SQL edit) and
// confirms loadTodosFromDB returns clamped, safe values rather than silently
// propagating the garbage.
func TestLoadTodosClampsCorruptRow(t *testing.T) {
	h := openTestDB(t)
	buf, restore := captureValidationWarnings(t)
	defer restore()

	_, err := h.Exec(`INSERT INTO todos
		(id, title, status, priority, size, project, parent_id,
		 created_at, modified_at, due_date, start_date,
		 notes, completed_at, sequence, data, deleted, deleted_at)
		VALUES ('corrupt-id', 'I came from chaos', 99, 42, -1, '', '',
		        '', '', '', '', '', '', 0, '', 0, '')`)
	if err != nil {
		t.Fatalf("insert corrupt row: %v", err)
	}

	got, err := loadTodosFromDB(h)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d todos, want 1", len(got))
	}
	if got[0].Status != todo.Pending {
		t.Errorf("status not clamped: %v", got[0].Status)
	}
	if got[0].Priority != todo.PriorityMedium {
		t.Errorf("priority not clamped: %v", got[0].Priority)
	}
	if got[0].Size != todo.SizeMedium {
		t.Errorf("size not clamped: %v", got[0].Size)
	}
	if !strings.Contains(buf.String(), "corrupt-id") {
		t.Errorf("expected warnings to reference the corrupt task id, got:\n%s", buf.String())
	}
}

// TestParseTimeWarnsOnMalformed: a non-empty timestamp that doesn't parse is
// corruption and must warn (loud like the enum clamps), while "" is the normal
// unset encoding and stays silent.
func TestParseTimeWarnsOnMalformed(t *testing.T) {
	buf, restore := captureValidationWarnings(t)
	defer restore()

	if !parseTime("").IsZero() {
		t.Errorf("empty string should parse to the zero time")
	}
	if buf.Len() > 0 {
		t.Fatalf("empty string is the unset encoding and must not warn, got:\n%s", buf.String())
	}
	if !parseTime("not-a-timestamp").IsZero() {
		t.Errorf("malformed timestamp should fall back to the zero time")
	}
	if !strings.Contains(buf.String(), "not-a-timestamp") {
		t.Errorf("malformed timestamp should warn with the offending value, got:\n%s", buf.String())
	}
	buf.Reset()
	if parseTime("2026-07-02T10:00:00.123456789Z").IsZero() {
		t.Errorf("RFC3339Nano must parse")
	}
	if buf.Len() > 0 {
		t.Errorf("valid timestamps must not warn, got:\n%s", buf.String())
	}
}
