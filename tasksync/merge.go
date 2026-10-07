package tasksync

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"sort"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
)

// merge.go is the heart of tjek's cross-device sync: a pure, I/O-free fold of
// two task sets into one authoritative set. `tjek serve` (and the `tjek sync`
// client) call into it; everything around it — HTTP, storage — is plumbing.
//
// Resolution rules:
//   - Tasks are matched by their UUID.
//   - Scalar fields, tags and dependencies go to the last writer, ordered by
//     eventTime (modification time, or deletion time for a tombstone); an exact
//     timestamp tie breaks on the higher content hash so the result is stable
//     and independent of argument order.
//   - Child collections (comments, time entries) merge independently
//     by their own UUIDs, so a child added or deleted on the side that loses the
//     scalar race is not silently dropped. A child tombstone is sticky.
//   - Tombstones (task and child) are retained, never pruned, so deletions keep
//     propagating instead of a stale device resurrecting the row.
//   - A task whose parent ends up missing or deleted is re-homed to top level.

// eventTime is the moment a task version was last acted on: deletion time for a
// tombstone, otherwise modification time. Two versions of the same task are
// ordered by it — later wins.
func eventTime(t todo.Todo) time.Time {
	if t.Deleted {
		return t.DeletedAt
	}
	return t.ModifiedAt
}

// hashGreater reports whether x sorts after y by the SHA-256 of its canonical
// JSON. It is the deterministic tiebreaker when two versions carry the same
// timestamp, keeping merges stable and order-independent.
func hashGreater[T any](x, y T) bool {
	bx, _ := json.Marshal(x)
	by, _ := json.Marshal(y)
	hx := sha256.Sum256(bx)
	hy := sha256.Sum256(by)
	return bytes.Compare(hx[:], hy[:]) > 0
}

// laterWins returns the surviving scalar version of two same-ID tasks.
func laterWins(a, b todo.Todo) todo.Todo {
	ta, tb := eventTime(a), eventTime(b)
	switch {
	case ta.After(tb):
		return a
	case tb.After(ta):
		return b
	default:
		if hashGreater(a, b) {
			return a
		}
		return b
	}
}

// mergeTask resolves two versions of the same task. Scalars, tags and deps come
// from the last writer; child collections merge independently so a comment or
// time entry touched on the losing side survives.
func mergeTask(a, b todo.Todo) todo.Todo {
	out := laterWins(a, b)
	out.Comments = mergeComments(a.Comments, b.Comments)
	out.TimeEntries = mergeTimeEntries(a.TimeEntries, b.TimeEntries)
	return out
}

// mergeChildren unions two child slices by ID. A tombstone on either side wins
// and is retained so the deletion keeps propagating; among live versions the
// later-modified one wins (an edit on one device beats the stale copy on the
// other), with the higher-hash one as the tiebreak so records written before
// ModifiedAt existed (both zero) still resolve stably. Order follows first
// appearance.
func mergeChildren[T any](a, b []T, id func(T) string, deletedAt func(T) time.Time, modified func(T) time.Time) []T {
	type slot struct {
		v        T
		isDel    bool
		haveLive bool
	}
	order := make([]string, 0, len(a)+len(b))
	slots := make(map[string]*slot, len(a)+len(b))
	consume := func(x T) {
		k := id(x)
		s, ok := slots[k]
		if !ok {
			s = &slot{}
			slots[k] = s
			order = append(order, k)
		}
		switch {
		case !deletedAt(x).IsZero():
			if s.isDel {
				// Both sides are tombstones: resolve like laterWins does for
				// tasks — later DeletedAt wins, hash tiebreak — so the result
				// is independent of argument order. Without this the second
				// argument always won, and two devices that each deleted the
				// same child (different DeletedAt) would flip the server's
				// store on every sync, ping-ponging forever.
				dx, ds := deletedAt(x), deletedAt(s.v)
				if dx.After(ds) || (dx.Equal(ds) && hashGreater(x, s.v)) {
					s.v = x
				}
				return
			}
			s.isDel = true
			s.v = x // keep the tombstone so deleted_at persists
		case s.isDel:
			// a deletion on either side is sticky — ignore the live version
		case !s.haveLive:
			s.v = x
			s.haveLive = true
		case modified(x).After(modified(s.v)):
			s.v = x
		case modified(x).Before(modified(s.v)):
			// keep s.v — it is the later edit
		case hashGreater(x, s.v):
			s.v = x
		}
	}
	for _, x := range a {
		consume(x)
	}
	for _, x := range b {
		consume(x)
	}
	var out []T
	for _, k := range order {
		out = append(out, slots[k].v)
	}
	return out
}

func mergeComments(a, b []todo.Comment) []todo.Comment {
	return mergeChildren(a, b,
		func(c todo.Comment) string { return c.ID },
		func(c todo.Comment) time.Time { return c.DeletedAt },
		func(c todo.Comment) time.Time { return c.ModifiedAt },
	)
}

// Time entries order by ModifiedAt like the others, but with a fallback for
// entries from before the field existed: a stopped entry beats a running copy
// of itself when neither carries a ModifiedAt, since a stop is always the
// later event.
func mergeTimeEntries(a, b []todo.TimeEntry) []todo.TimeEntry {
	return mergeChildren(a, b,
		func(e todo.TimeEntry) string { return e.ID },
		func(e todo.TimeEntry) time.Time { return e.DeletedAt },
		func(e todo.TimeEntry) time.Time {
			if !e.ModifiedAt.IsZero() {
				return e.ModifiedAt
			}
			return e.StoppedAt // zero for a running legacy entry → stop wins
		},
	)
}

// Merge folds two task sets into one authoritative set. It is symmetric in its
// arguments. Tombstones are retained; a task whose parent is missing or deleted
// is re-homed to top level; output is sorted by ID for a stable wire order.
func Merge(server, client []todo.Todo) []todo.Todo {
	merged := make(map[string]todo.Todo, len(server)+len(client))
	for _, t := range server {
		merged[t.ID] = t
	}
	for _, t := range client {
		if existing, ok := merged[t.ID]; ok {
			merged[t.ID] = mergeTask(existing, t)
		} else {
			merged[t.ID] = t
		}
	}
	for id, t := range merged {
		if t.Deleted || t.ParentID == "" {
			continue
		}
		if parent, ok := merged[t.ParentID]; !ok || parent.Deleted {
			t.ParentID = ""
			merged[id] = t
		}
	}
	breakParentCycles(merged)
	out := make([]todo.Todo, 0, len(merged))
	for _, t := range merged {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// breakParentCycles guarantees every ParentID chain terminates, so the
// parent-chain walkers (sequence-score rollup, ancestor auto-close) can never
// spin forever. The re-home pass above already cuts tasks whose parent is
// missing or deleted; this covers the one remaining case — a chain that loops
// back on itself (A→B→A) without ever hitting a missing/deleted parent. Such a
// cycle can't be created through the UI, but a corrupt store or a buggy/hostile
// peer could send one. Each task has exactly one parent, so the parent graph is
// functional and every component holds at most one cycle; re-homing the
// highest-ID member of each cycle to top level (deterministic, independent of
// map order) is enough to make the whole set acyclic.
func breakParentCycles(merged map[string]todo.Todo) {
	for id := range merged {
		seen := make(map[string]bool)
		for cur := id; cur != ""; {
			if seen[cur] {
				cut := highestInCycle(merged, cur)
				t := merged[cut]
				t.ParentID = ""
				merged[cut] = t
				break
			}
			seen[cur] = true
			t, ok := merged[cur]
			if !ok {
				break
			}
			cur = t.ParentID
		}
	}
}

// highestInCycle returns the greatest task ID on the cycle that contains start,
// walking the loop once from start back to itself. start is assumed to lie on a
// cycle (it's the node a chain walk reached twice).
func highestInCycle(merged map[string]todo.Todo, start string) string {
	hi := start
	for cur := merged[start].ParentID; cur != start && cur != ""; {
		if cur > hi {
			hi = cur
		}
		t, ok := merged[cur]
		if !ok {
			break
		}
		cur = t.ParentID
	}
	return hi
}
