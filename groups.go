package main

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Morteningemann86/xtratjek/rank"
	"github.com/Morteningemann86/xtratjek/todo"
)

// ── Tags and Projects as one kind of thing ───────────────────────────────────
//
// A tag and a project are both a named group of tasks, and the two tabs answer
// the same questions about one: how much is still open, when it last moved,
// and what to do next in it. The summary, the ordering, the task list behind a
// row and the rule for hiding finished groups are therefore shared, and each
// tab only says how a task maps onto its groups (tagGroupKeys,
// projectGroupKeys).
//
// Finished work is kept out of the way rather than out of reach: a group with
// nothing open is hidden until h asks for it, and inside a group the done tasks
// fold into one line under the open ones. Most of what a long-lived store
// holds is finished, and giving it equal space buried the few rows still in
// play.

// groupSort is the order the Tags and Projects lists are drawn in.
type groupSort int

const (
	// groupSortOpen puts the groups with the most open work first — the
	// default, since that is what the tabs are for.
	groupSortOpen groupSort = iota
	groupSortRecent
	groupSortName
	groupSortCount
)

func (s groupSort) next() groupSort { return (s + 1) % groupSortCount }

func (s groupSort) label() string {
	switch s {
	case groupSortRecent:
		return tr("recent")
	case groupSortName:
		return tr("alpha")
	default:
		return tr("open")
	}
}

// groupSummary is everything a Tags or Projects row says about its group.
type groupSummary struct {
	open, overdue, done int
	// last is the newest ModifiedAt among the group's tasks: an edit, a
	// completion or a time entry all move it.
	last time.Time
	// next is the open task the sequencer ranks highest in the group.
	nextID    string
	nextTitle string
	nextScore float64
}

func (s *groupSummary) finished() bool { return s.open == 0 }

// tagGroupKeys visits the tag groups t belongs to. A top-level task without
// tags belongs to the virtual (untagged) row; an untagged subtask belongs to
// its parent, so it is nobody's triage problem and counts nowhere.
func tagGroupKeys(t *todo.Todo, visit func(string)) {
	if len(t.Tags) == 0 {
		if t.ParentID == "" {
			visit(untaggedKey)
		}
		return
	}
	for _, tag := range t.Tags {
		visit(tag)
	}
}

func projectGroupKeys(t *todo.Todo, visit func(string)) {
	if t.Project != "" {
		visit(t.Project)
	}
}

// inTagGroup and inProjectGroup are the membership tests behind a drilled-in
// list, and must agree with the key functions above or a row's counts would
// describe a list other than the one enter opens.
func inTagGroup(t *todo.Todo, key string) bool {
	if key == untaggedKey {
		return len(t.Tags) == 0 && t.ParentID == ""
	}
	for _, tag := range t.Tags {
		if tag == key {
			return true
		}
	}
	return false
}

func inProjectGroup(t *todo.Todo, key string) bool { return t.Project == key }

// summarizeGroups builds one summary per group in a single pass over the task
// set. score ranks the open tasks for "next up"; pass a frozen one
// (rank.Ranker.ScoreNow) so equal tasks tie and the ID decides.
func summarizeGroups(all []*todo.Todo, keys func(*todo.Todo, func(string)), score func(*todo.Todo) float64) map[string]*groupSummary {
	out := make(map[string]*groupSummary)
	for _, t := range all {
		var s float64
		scored := false
		keys(t, func(key string) {
			g := out[key]
			if g == nil {
				g = &groupSummary{}
				out[key] = g
			}
			if t.ModifiedAt.After(g.last) {
				g.last = t.ModifiedAt
			}
			if t.Status == todo.Done {
				g.done++
				return
			}
			g.open++
			if t.IsOverdue() {
				g.overdue++
			}
			if !scored {
				s, scored = score(t), true
			}
			if g.nextID == "" || s > g.nextScore || (s == g.nextScore && t.ID < g.nextID) {
				g.nextID, g.nextTitle, g.nextScore = t.ID, t.Title, s
			}
		})
	}
	return out
}

// sortGroupKeys orders group names for display. Every mode ends at the name so
// the list never reshuffles between two frames over the same data.
func sortGroupKeys(keys []string, mode groupSort, sums map[string]*groupSummary) {
	byName := func(a, b string) bool {
		if la, lb := strings.ToLower(a), strings.ToLower(b); la != lb {
			return la < lb
		}
		return a < b
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		sa, sb := sums[a], sums[b]
		if sa == nil || sb == nil {
			return byName(a, b)
		}
		switch mode {
		case groupSortOpen:
			if sa.open != sb.open {
				return sa.open > sb.open
			}
			if !sa.last.Equal(sb.last) {
				return sa.last.After(sb.last)
			}
		case groupSortRecent:
			if !sa.last.Equal(sb.last) {
				return sa.last.After(sb.last)
			}
		}
		return byName(a, b)
	})
}

// visibleGroups is the list a Tags or Projects tab draws: the groups matching
// the tab's filter, sorted, with the finished ones left out unless shown.
// pinned stays visible regardless — it is the group the cursor is inside, and
// finishing its last open task must not pull the list out from under you.
// The (untagged) row sorts like any other group, so the list reads in the
// order its title names; by name its key sorts ahead of every tag.
func visibleGroups(sums map[string]*groupSummary, mode groupSort, showFinished bool, pinned string, match func(string) bool) []string {
	keys := make([]string, 0, len(sums))
	for key, s := range sums {
		if !showFinished && s.finished() && key != pinned {
			continue
		}
		if !match(key) {
			continue
		}
		keys = append(keys, key)
	}
	sortGroupKeys(keys, mode, sums)
	return keys
}

// hiddenFinishedGroups counts the finished groups visibleGroups is leaving out.
func hiddenFinishedGroups(sums map[string]*groupSummary, showFinished bool, match func(string) bool) int {
	if showFinished {
		return 0
	}
	n := 0
	for key, s := range sums {
		if s.finished() && match(key) {
			n++
		}
	}
	return n
}

// groupTaskList is the task list behind a group's row, in the order the
// drilled-in list walks it: open tasks first, highest ranked first; then, only
// when finished work is shown, the done tasks newest first. Subtasks fold the
// way they do on the Tasks tab, sharing its expandedTasks: an unfolded
// parent is followed by all of its subtasks, whichever group those carry, and
// a folded one hides them. A subtask whose parent is not open in the group
// stands as a row of its own.
func (m model) groupTaskList(match func(*todo.Todo) bool) []todo.Todo {
	var open, done []*todo.Todo
	inOpen := make(map[string]bool)
	for _, t := range m.tasks {
		if !match(t) {
			continue
		}
		if t.Status == todo.Done {
			done = append(done, t)
			continue
		}
		open = append(open, t)
		inOpen[t.ID] = true
	}
	roots := open[:0:0]
	for _, t := range open {
		if t.ParentID == "" || !inOpen[t.ParentID] {
			roots = append(roots, t)
		}
	}
	score := m.rank.ScoreNow()
	ranked := make(map[string]float64, len(roots))
	for _, t := range roots {
		ranked[t.ID] = rank.ScoreOf(t, m.cache.rankScore, score)
	}
	sort.Slice(roots, func(i, j int) bool {
		if ri, rj := ranked[roots[i].ID], ranked[roots[j].ID]; ri != rj {
			return ri > rj
		}
		return roots[i].ID < roots[j].ID
	})

	out := make([]todo.Todo, 0, len(open)+len(done))
	listed := make(map[string]bool)
	for _, t := range roots {
		out = append(out, *t)
		if !m.expandedTasks[t.ID] {
			continue
		}
		for _, id := range m.subtaskIDs(t.ID) {
			if sub := m.get(id); sub != nil {
				out = append(out, *sub)
				listed[id] = true
			}
		}
	}
	if m.showFinishedGroups {
		sort.Slice(done, func(i, j int) bool {
			if !done[i].CompletedAt.Equal(done[j].CompletedAt) {
				return done[i].CompletedAt.After(done[j].CompletedAt)
			}
			return done[i].ID < done[j].ID
		})
		for _, t := range done {
			if !listed[t.ID] {
				out = append(out, *t)
			}
		}
	}
	return out
}

// groupNestedRows reports, per row of a groupTaskList, whether it is drawn
// indented as a subtask of the unfolded parent above it. An unfolded parent's
// subtasks are listed straight after it and nowhere else, so a row is nested
// exactly when it continues that run.
func (m model) groupNestedRows(tasks []todo.Todo) []bool {
	nested := make([]bool, len(tasks))
	for i := 1; i < len(tasks); i++ {
		p, prev := tasks[i].ParentID, &tasks[i-1]
		nested[i] = p != "" && m.expandedTasks[p] &&
			(prev.ID == p || (nested[i-1] && prev.ParentID == p))
	}
	return nested
}

// hasDatedOpenTask reports whether a timeline has anything ahead of it to
// draw: an open task with a start or a due date. Without one the chart is only
// a record of when things were finished.
func hasDatedOpenTask(tasks []todo.Todo) bool {
	for i := range tasks {
		t := &tasks[i]
		if t.Status != todo.Done && (!t.StartDate.IsZero() || !t.DueDate.IsZero()) {
			return true
		}
	}
	return false
}

// formatSince renders how long ago a group last moved, in the fewest cells
// that still read at a glance.
func formatSince(at, now time.Time) string {
	if at.IsZero() {
		return "-"
	}
	days := int(startOfDay(now).Sub(startOfDay(at)).Hours() / 24)
	switch {
	case days <= 0:
		return tr("today")
	case days < 14:
		return strconv.Itoa(days) + "d"
	case days < 70:
		return strconv.Itoa(days/7) + "w"
	default:
		return strconv.Itoa(days/30) + "mo"
	}
}
