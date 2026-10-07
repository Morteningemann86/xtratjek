package tasksync

import (
	"reflect"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
)

// merge_test.go is the full conflict-resolution table for the sync merge core.
// Everything here is pure (no store, no HTTP), so it runs in microseconds.

var mBase = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

func at(d time.Duration) time.Time { return mBase.Add(d) }

func mkTask(id, title string, modified time.Time) todo.Todo {
	return todo.Todo{ID: id, Title: title, CreatedAt: mBase, ModifiedAt: modified}
}

func mkTomb(id string, deletedAt time.Time) todo.Todo {
	return todo.Todo{ID: id, CreatedAt: mBase, Deleted: true, DeletedAt: deletedAt}
}

func mkComment(id, text string, created time.Time) todo.Comment {
	return todo.Comment{ID: id, Text: text, CreatedAt: created}
}

func delComment(id string, deletedAt time.Time) todo.Comment {
	return todo.Comment{ID: id, DeletedAt: deletedAt}
}

func indexByID(ts []todo.Todo) map[string]todo.Todo {
	m := make(map[string]todo.Todo, len(ts))
	for _, t := range ts {
		m[t.ID] = t
	}
	return m
}

// ── Scalar LWW + delete-vs-edit ────────────────────────────────────────────────

func TestMergeTaskScalarAndDelete(t *testing.T) {
	const h = time.Hour
	tests := []struct {
		name        string
		a, b        todo.Todo
		wantTitle   string
		wantDeleted bool
		wantDelAt   time.Time // checked only when non-zero
	}{
		{"newer scalar wins", mkTask("x", "old", at(0)), mkTask("x", "new", at(h)), "new", false, time.Time{}},
		{"delete newer than edit -> tombstone", mkTomb("x", at(2*h)), mkTask("x", "edit", at(h)), "", true, at(2 * h)},
		{"edit newer than delete -> resurrect", mkTomb("x", at(h)), mkTask("x", "edit", at(2*h)), "edit", false, time.Time{}},
		{"both deleted -> later tombstone", mkTomb("x", at(h)), mkTomb("x", at(2*h)), "", true, at(2 * h)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeTask(tt.a, tt.b)
			if got.Deleted != tt.wantDeleted {
				t.Fatalf("Deleted = %v, want %v", got.Deleted, tt.wantDeleted)
			}
			if !tt.wantDeleted && got.Title != tt.wantTitle {
				t.Errorf("Title = %q, want %q", got.Title, tt.wantTitle)
			}
			if !tt.wantDelAt.IsZero() && !got.DeletedAt.Equal(tt.wantDelAt) {
				t.Errorf("DeletedAt = %v, want %v", got.DeletedAt, tt.wantDelAt)
			}
		})
	}
}

func TestMergeTieBreakDeterministic(t *testing.T) {
	a := mkTask("x", "aaa", at(time.Hour))
	b := mkTask("x", "bbb", at(time.Hour))
	ab := mergeTask(a, b)
	ba := mergeTask(b, a)
	if ab.Title != ba.Title {
		t.Fatalf("tie not order-independent: %q vs %q", ab.Title, ba.Title)
	}
	if ab.Title != "aaa" && ab.Title != "bbb" {
		t.Fatalf("winner not one of the inputs: %q", ab.Title)
	}
}

// ── Children: the split-merge core ─────────────────────────────────────────────

func TestMergeChildKeepsConcurrentAdd(t *testing.T) {
	a := mkTask("x", "old", at(0))
	a.Comments = []todo.Comment{mkComment("c1", "hello", at(0))}
	b := mkTask("x", "new", at(time.Hour)) // scalar winner, no comments
	got := mergeTask(a, b)
	if got.Title != "new" {
		t.Errorf("Title = %q, want new", got.Title)
	}
	if len(got.Comments) != 1 || got.Comments[0].ID != "c1" {
		t.Fatalf("comment c1 dropped by scalar loss: %+v", got.Comments)
	}
}

func TestMergeChildDeleteSticky(t *testing.T) {
	a := mkTask("x", "keep", at(time.Hour))
	a.Comments = []todo.Comment{delComment("c2", at(time.Hour))} // deleted here
	b := mkTask("x", "new", at(2*time.Hour))                     // wins scalar, has c2 live
	b.Comments = []todo.Comment{mkComment("c2", "Y", at(0))}
	got := mergeTask(a, b)
	if got.Title != "new" {
		t.Errorf("Title = %q, want new", got.Title)
	}
	if len(got.Comments) != 1 {
		t.Fatalf("want tombstone retained (1), got %d", len(got.Comments))
	}
	if got.Comments[0].DeletedAt.IsZero() {
		t.Errorf("c2 should stay deleted, got live: %+v", got.Comments[0])
	}
}

func TestMergeChildBothAddDifferent(t *testing.T) {
	a := mkTask("x", "t", at(0))
	a.Comments = []todo.Comment{mkComment("c1", "A", at(0))}
	b := mkTask("x", "t", at(0))
	b.Comments = []todo.Comment{mkComment("c2", "B", at(0))}
	got := mergeTask(a, b)
	ids := map[string]bool{}
	for _, c := range got.Comments {
		ids[c.ID] = true
	}
	if len(got.Comments) != 2 || !ids["c1"] || !ids["c2"] {
		t.Fatalf("want c1+c2 unioned, got %+v", got.Comments)
	}
}

func TestMergeChildTombstoneBeatsLive(t *testing.T) {
	a := mkTask("x", "t", at(0))
	a.Comments = []todo.Comment{mkComment("c1", "A", at(0))}
	b := mkTask("x", "t", at(0))
	b.Comments = []todo.Comment{delComment("c1", at(time.Hour))}
	got := mergeTask(a, b)
	if len(got.Comments) != 1 || got.Comments[0].DeletedAt.IsZero() {
		t.Fatalf("c1 should be tombstoned, got %+v", got.Comments)
	}
}

func TestMergeChildBothTombstonesSymmetric(t *testing.T) {
	// Both devices deleted the same comment with different DeletedAt. The
	// resolution must be argument-order independent — the later tombstone wins
	// either way — or each client's sync flips the server's copy back and
	// forth, and the resulting write+broadcast per sync ping-pongs forever.
	a := []todo.Comment{delComment("c1", at(time.Hour))}
	b := []todo.Comment{delComment("c1", at(2*time.Hour))}
	ab := mergeComments(a, b)
	ba := mergeComments(b, a)
	if len(ab) != 1 || len(ba) != 1 {
		t.Fatalf("want 1 tombstone each, got %d / %d", len(ab), len(ba))
	}
	if !ab[0].DeletedAt.Equal(ba[0].DeletedAt) {
		t.Fatalf("asymmetric tombstone merge: a,b -> %v; b,a -> %v", ab[0].DeletedAt, ba[0].DeletedAt)
	}
	if !ab[0].DeletedAt.Equal(at(2 * time.Hour)) {
		t.Errorf("later tombstone should win, got DeletedAt=%v", ab[0].DeletedAt)
	}
}

// ── Tags / deps follow the scalar winner (LWW) ─────────────────────────────────

func TestMergeTagsFollowScalarWinner(t *testing.T) {
	// Older side added #x; the newer edit wins and its tag set (without #x) applies.
	a := mkTask("x", "old", at(0))
	a.Tags = []string{"x"}
	b := mkTask("x", "new", at(time.Hour))
	if got := mergeTask(a, b); len(got.Tags) != 0 {
		t.Errorf("added-on-loser tag should be dropped by LWW, got %v", got.Tags)
	}

	// Newer side removed #y; the removal propagates.
	c := mkTask("x", "newer", at(2*time.Hour)) // tag removed here
	d := mkTask("x", "old", at(time.Hour))
	d.Tags = []string{"y"}
	for _, tg := range mergeTask(c, d).Tags {
		if tg == "y" {
			t.Errorf("removal of #y should propagate, still present")
		}
	}
}

// ── Whole-set Merge: orphans, propagation, order, idempotency ──────────────────

func TestMergeReHomesOrphan(t *testing.T) {
	parent := mkTomb("p", at(2*time.Hour))
	child := mkTask("c", "child", at(time.Hour))
	child.ParentID = "p"
	got := indexByID(Merge([]todo.Todo{parent}, []todo.Todo{child}))
	if got["c"].ParentID != "" {
		t.Errorf("orphan not re-homed, ParentID = %q", got["c"].ParentID)
	}
	if !got["p"].Deleted {
		t.Errorf("parent tombstone lost")
	}
}

func TestMergeKeepsLiveParentLink(t *testing.T) {
	parent := mkTask("p", "P", at(time.Hour))
	child := mkTask("c", "C", at(time.Hour))
	child.ParentID = "p"
	got := indexByID(Merge([]todo.Todo{parent}, []todo.Todo{child}))
	if got["c"].ParentID != "p" {
		t.Errorf("live parent link dropped")
	}
}

// A corrupt store or hostile peer could send a ParentID chain that loops back
// on itself; Merge must break it so the parent-chain walkers can't hang. The
// cut is deterministic — the highest-ID member of the cycle is re-homed.
func TestMergeBreaksParentCycle(t *testing.T) {
	t.Run("mutual A<->B cycle", func(t *testing.T) {
		a := mkTask("a", "A", at(time.Hour))
		a.ParentID = "b"
		b := mkTask("b", "B", at(time.Hour))
		b.ParentID = "a"
		got := indexByID(Merge([]todo.Todo{a, b}, nil))
		// Highest ID ("b") is cut; "a" keeps its link to the now-rooted "b".
		if got["b"].ParentID != "" {
			t.Errorf("highest-ID cycle member not re-homed, b.ParentID = %q", got["b"].ParentID)
		}
		if got["a"].ParentID != "b" {
			t.Errorf("non-cut member should keep its link, a.ParentID = %q", got["a"].ParentID)
		}
	})
	t.Run("self-parent", func(t *testing.T) {
		a := mkTask("a", "A", at(time.Hour))
		a.ParentID = "a"
		got := indexByID(Merge([]todo.Todo{a}, nil))
		if got["a"].ParentID != "" {
			t.Errorf("self-parent not re-homed, a.ParentID = %q", got["a"].ParentID)
		}
	})
}

func TestMergeDeletionPropagates(t *testing.T) {
	got := indexByID(Merge(
		[]todo.Todo{mkTomb("x", at(2*time.Hour))},             // server deleted later
		[]todo.Todo{mkTask("x", "resurrect?", at(time.Hour))}, // client's stale edit
	))
	if !got["x"].Deleted {
		t.Errorf("newer deletion should win over older edit")
	}
}

func TestMergeUnionAndStableOrder(t *testing.T) {
	got := Merge(
		[]todo.Todo{mkTask("b", "B", at(0))},
		[]todo.Todo{mkTask("a", "A", at(0))},
	)
	if len(got) != 2 {
		t.Fatalf("want 2 tasks unioned, got %d", len(got))
	}
	if got[0].ID != "a" || got[1].ID != "b" {
		t.Errorf("output not sorted by ID: %s, %s", got[0].ID, got[1].ID)
	}
}

// A child edited later must win over the stale copy from the other device,
// in both argument orders — recency, not the hash coin flip, decides.
func TestMergeChildEditWinsByRecency(t *testing.T) {
	older := mkComment("c1", "first wording", at(0))
	older.ModifiedAt = at(0)
	newer := mkComment("c1", "edited wording", at(0))
	newer.ModifiedAt = at(time.Hour)

	for _, dir := range []struct {
		name string
		a, b []todo.Comment
	}{
		{"newer second", []todo.Comment{older}, []todo.Comment{newer}},
		{"newer first", []todo.Comment{newer}, []todo.Comment{older}},
	} {
		got := mergeComments(dir.a, dir.b)
		if len(got) != 1 || got[0].Text != "edited wording" {
			t.Errorf("%s: got %+v, want the later edit to win", dir.name, got)
		}
	}
}

// A stopped time entry must beat the still-running copy of itself, both via
// ModifiedAt (new records) and via the stopped_at fallback (records from
// before ModifiedAt existed). Otherwise a stop can lose the merge and the
// timer resurrects on every device.
func TestMergeStoppedTimerBeatsRunningCopy(t *testing.T) {
	running := todo.TimeEntry{ID: "e1", StartedAt: at(0)}
	stopped := todo.TimeEntry{ID: "e1", StartedAt: at(0), StoppedAt: at(time.Hour)}

	for _, tc := range []struct {
		name    string
		stamped bool
	}{
		{"legacy entries (no ModifiedAt)", false},
		{"stamped entries", true},
	} {
		s := stopped
		if tc.stamped {
			s.ModifiedAt = at(time.Hour)
		}
		for _, pair := range [][2][]todo.TimeEntry{
			{{running}, {s}},
			{{s}, {running}},
		} {
			got := mergeTimeEntries(pair[0], pair[1])
			if len(got) != 1 || got[0].StoppedAt.IsZero() {
				t.Errorf("%s: got %+v, want the stopped entry to win", tc.name, got)
			}
		}
	}
}

func TestMergeIdempotent(t *testing.T) {
	a := mkTask("a", "A", at(time.Hour))
	a.Comments = []todo.Comment{mkComment("c1", "x", at(0))}
	a.Tags = []string{"t"}
	x := []todo.Todo{a, mkTomb("b", at(time.Hour))}

	once := Merge(x, nil)
	twice := Merge(once, once)
	if !reflect.DeepEqual(once, twice) {
		t.Fatalf("merge not idempotent:\n once  = %+v\n twice = %+v", once, twice)
	}
}

// ── Slow-clock regression ──────────────────────────────────────────────────────

// TestSlowClockEditWinsMerge is a regression test for the slow-clock merge-loss
// bug. Before the fix, a device with a clock behind the task's current
// ModifiedAt would stamp its edit with an older timestamp, causing the merge to
// treat the edit as a stale version and silently discard it.
//
// The fix — StampModified(prev) = max(now, prev+1ms) — ensures the stamp is
// always strictly after the version being replaced, regardless of the local
// clock's relation to the previous timestamp.
//
// We simulate the scenario directly on todo.Todo values (no storage or HTTP):
//  1. A "fast device" edits the task, stamping a future ModifiedAt (ahead of now).
//  2. A "slow device" then mutates the task using StampModified(fast.ModifiedAt),
//     which should produce fast.ModifiedAt+1ms even though the wall clock is behind.
//  3. Merge must pick the slow device's edit.
func TestSlowClockEditWinsMerge(t *testing.T) {
	// Step 1: fast device stamps a future ModifiedAt.
	fastModified := time.Now().Add(30 * time.Second) // fast clock: 30s ahead
	fastVersion := todo.Todo{
		ID:         "task-1",
		Title:      "original title",
		CreatedAt:  mBase,
		ModifiedAt: fastModified,
	}

	// Step 2: slow device's edit — simulate StampModified as the mutation path
	// now uses it. The slow device's wall clock is behind fastModified, but
	// StampModified clamps to fastModified+1ms.
	slowEdited := fastVersion                                // start from the same version
	slowEdited.Title = "edited by slow device"               // the edit
	slowEdited.ModifiedAt = todo.StampModified(fastModified) // clamp against prev
	wantModified := fastModified.Add(time.Millisecond)
	if !slowEdited.ModifiedAt.Equal(wantModified) {
		t.Fatalf("StampModified did not clamp: got %v, want %v", slowEdited.ModifiedAt, wantModified)
	}

	// Step 3: merge — slow device's edit must win over fast device's version.
	got := indexByID(Merge([]todo.Todo{fastVersion}, []todo.Todo{slowEdited}))
	if got["task-1"].Title != "edited by slow device" {
		t.Errorf("slow-clock edit lost to fast-device version: title = %q, want %q",
			got["task-1"].Title, "edited by slow device")
	}
	if !got["task-1"].ModifiedAt.Equal(wantModified) {
		t.Errorf("merged ModifiedAt = %v, want %v", got["task-1"].ModifiedAt, wantModified)
	}
}

// TestSlowClockDeleteWinsMerge is the deletion twin of
// TestSlowClockEditWinsMerge: the merge resolves delete-vs-edit by comparing
// the tombstone's DeletedAt against the live version's ModifiedAt (eventTime),
// so a slow-clock device stamping an unclamped wall-clock DeletedAt loses to
// the very version it deleted and the task resurrects on the next sync. The
// writer (saveNormalizedIn) now stamps deleted_at via StampModified of the
// row's modified_at; this proves that stamp wins the merge in both argument
// orders.
func TestSlowClockDeleteWinsMerge(t *testing.T) {
	// Fast device's edit: ModifiedAt ahead of the slow device's wall clock.
	fastModified := time.Now().Add(30 * time.Second)
	liveVersion := todo.Todo{
		ID:         "task-1",
		Title:      "deleted on the slow device",
		CreatedAt:  mBase,
		ModifiedAt: fastModified,
	}

	// Slow device deletes that version. Its wall clock is behind fastModified,
	// but the tombstone writer clamps: DeletedAt = StampModified(fastModified).
	tomb := liveVersion
	tomb.Deleted = true
	tomb.DeletedAt = todo.StampModified(fastModified)
	if !tomb.DeletedAt.After(fastModified) {
		t.Fatalf("StampModified did not clamp: DeletedAt = %v, not after %v", tomb.DeletedAt, fastModified)
	}

	for _, dir := range []struct {
		name string
		a, b []todo.Todo
	}{
		{"tombstone second", []todo.Todo{liveVersion}, []todo.Todo{tomb}},
		{"tombstone first", []todo.Todo{tomb}, []todo.Todo{liveVersion}},
	} {
		got := indexByID(Merge(dir.a, dir.b))
		if !got["task-1"].Deleted {
			t.Errorf("%s: slow-clock deletion lost to the version it deleted — task resurrected", dir.name)
		}
	}
}
