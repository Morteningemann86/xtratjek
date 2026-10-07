package main

import (
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
)

// taskops.go — task-tree operations shared by the TUI model and the CLI.
// Each operation is parameterized by lookup funcs instead of a concrete
// container: the model passes its maintained indexes (m.subtaskIDs / m.get),
// the CLI passes closures over the loaded slice (sliceTaskLookups). One
// implementation, two data sources — the two surfaces can't drift apart.

// sliceTaskLookups builds the (children, get) lookup pair over a loaded task
// slice. The returned pointers alias the slice's elements, so mutations made
// through get are visible to the caller's slice (and safe to hand to Save).
func sliceTaskLookups(todos []todo.Todo) (children func(string) []string, get func(string) *todo.Todo) {
	byID := make(map[string]*todo.Todo, len(todos))
	kids := make(map[string][]string, len(todos))
	for i := range todos {
		byID[todos[i].ID] = &todos[i]
		if pid := todos[i].ParentID; pid != "" {
			kids[pid] = append(kids[pid], todos[i].ID)
		}
	}
	return func(id string) []string { return kids[id] }, func(id string) *todo.Todo { return byID[id] }
}

// descendantIDsFrom returns rootID followed by every transitive subtask ID in
// BFS order. Used by cascade-delete on both surfaces: every descendant must be
// tombstoned alongside the root, otherwise children survive with a ParentID
// pointing at a deleted task.
func descendantIDsFrom(children func(string) []string, rootID string) []string {
	out := []string{rootID}
	for i := 0; i < len(out); i++ {
		out = append(out, children(out[i])...)
	}
	return out
}

// cloneSubtreeResetFrom builds a fresh Pending copy of every descendant of
// srcParentID, reparented under newParentID, with each clone's history wiped
// (todo.NewSubtask starts clean) and DueDate/StartDate shifted by delta so the
// subtree's internal scheduling stays relative to the new parent. BFS with
// (srcID, newParentID) pairs so nested grandchildren land under their
// freshly-cloned parent rather than the recurring root. Returns the clones;
// the caller stores them (model.add / repo.Save).
func cloneSubtreeResetFrom(children func(string) []string, get func(string) *todo.Todo,
	srcParentID, newParentID string, delta time.Duration,
) []todo.Todo {
	var out []todo.Todo
	type pair struct{ srcID, newPID string }
	queue := []pair{{srcParentID, newParentID}}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, childID := range children(p.srcID) {
			child := get(childID)
			if child == nil {
				continue
			}
			clone := todo.NewSubtask(child.Title, p.newPID)
			clone.Priority = child.Priority
			clone.Size = child.Size
			clone.Project = child.Project
			clone.Notes = child.Notes
			clone.Recurrence = child.Recurrence
			if len(child.Tags) > 0 {
				clone.Tags = append([]string{}, child.Tags...)
			}
			if !child.DueDate.IsZero() {
				clone.DueDate = child.DueDate.Add(delta)
			}
			if !child.StartDate.IsZero() {
				clone.StartDate = child.StartDate.Add(delta)
			}
			out = append(out, clone)
			queue = append(queue, pair{child.ID, clone.ID})
		}
	}
	return out
}

// extendAncestorsDue walks up from child via ParentID, bumping each ancestor's
// DueDate forward to at least match the child's. Only extends — never shrinks
// an ancestor's date. Mutates through the pointers get returns and reports the
// bumped ancestors so the caller can mark them dirty / include them in a save.
func extendAncestorsDue(get func(string) *todo.Todo, child *todo.Todo) []*todo.Todo {
	var bumped []*todo.Todo
	cur := child
	for cur != nil && cur.ParentID != "" {
		parent := get(cur.ParentID)
		if parent == nil {
			break
		}
		if cur.DueDate.IsZero() {
			break
		}
		if !parent.DueDate.IsZero() && !parent.DueDate.Before(cur.DueDate) {
			break
		}
		parent.SetDueDate(cur.DueDate)
		bumped = append(bumped, parent)
		cur = parent
	}
	return bumped
}

// propagateDescendantsDue copies parent's DueDate to every live descendant,
// including a zero date when the parent deadline is cleared. Mutates through
// get and reports only descendants whose date actually changed so callers can
// mark/save the exact affected set.
func propagateDescendantsDue(children func(string) []string, get func(string) *todo.Todo, parent *todo.Todo) []*todo.Todo {
	if parent == nil {
		return nil
	}
	var changed []*todo.Todo
	for _, id := range descendantIDsFrom(children, parent.ID)[1:] {
		child := get(id)
		if child == nil || child.Deleted || child.DueDate.Equal(parent.DueDate) {
			continue
		}
		child.SetDueDate(parent.DueDate)
		changed = append(changed, child)
	}
	return changed
}

// A subtask belongs to its parent's project: it is a step of that work.
// Creating one copies the project (InheritContextFrom), and a parent moved to
// another project — or out of one — takes its whole subtree along.

// propagateDescendantsProject copies parent's project, including a cleared
// one, to every live descendant and returns the ones it changed.
func propagateDescendantsProject(children func(string) []string, get func(string) *todo.Todo, parent *todo.Todo) []*todo.Todo {
	if parent == nil {
		return nil
	}
	var changed []*todo.Todo
	for _, id := range descendantIDsFrom(children, parent.ID)[1:] {
		child := get(id)
		if child == nil || child.Deleted || child.Project == parent.Project {
			continue
		}
		child.SetProject(parent.Project)
		changed = append(changed, child)
	}
	return changed
}

// stopOtherRunningTimers stops every running timer except exceptID, returning
// the touched tasks for the save set. This is the single-running-timer
// invariant the TUI's toggleTimer enforces, shared by the CLI paths
// (add --start, start) so the rule can't fork between surfaces.
func stopOtherRunningTimers(todos []todo.Todo, exceptID string) []*todo.Todo {
	var stopped []*todo.Todo
	for i := range todos {
		if todos[i].ID == exceptID {
			continue
		}
		if todos[i].IsTimerRunning() {
			todos[i].StopTimer()
			stopped = append(stopped, &todos[i])
		}
	}
	return stopped
}

// Priority is a statement about a whole subtree: a task the user has marked
// low is low including everything it is made of, so a subtask must never
// outrank its parent. Unlike deadlines — where a child that slips pushes its
// ancestors out (extendAncestorsDue) — priority is clamped *downward* only.
// Raising a subtask never lifts the parent the user deliberately parked; the
// child is capped instead, and a parent moved down takes its subtree with it.
// Both directions are enforced on every edit, on both surfaces, so the
// invariant holds no matter which end of the tree the user touched.

// clampPriorityToParent caps t at its parent's priority, reporting whether it
// had to. A missing or tombstoned parent leaves t alone: an orphan has no cap.
func clampPriorityToParent(get func(string) *todo.Todo, t *todo.Todo) bool {
	if t == nil || t.ParentID == "" {
		return false
	}
	parent := get(t.ParentID)
	if parent == nil || parent.Deleted || t.Priority <= parent.Priority {
		return false
	}
	t.SetPriority(parent.Priority)
	return true
}

// clampDescendantsPriority caps every live descendant of parent at parent's
// priority and returns the ones it changed, for dirty tracking / the save set.
// Capping the whole subtree against the root is enough: the parent's priority
// is the subtree's ceiling, and deeper caps already hold among themselves.
func clampDescendantsPriority(children func(string) []string, get func(string) *todo.Todo, parent *todo.Todo) []*todo.Todo {
	if parent == nil {
		return nil
	}
	var changed []*todo.Todo
	for _, id := range descendantIDsFrom(children, parent.ID)[1:] {
		child := get(id)
		if child == nil || child.Deleted || child.Priority <= parent.Priority {
			continue
		}
		child.SetPriority(parent.Priority)
		changed = append(changed, child)
	}
	return changed
}
