package main

import (
	"slices"
	"testing"

	"github.com/Morteningemann86/xtratjek/todo"
)

// A subtask belongs to its parent's project: moving the parent takes the whole
// subtree along, as one undo step.
func TestParentProjectCarriesToSubtasks(t *testing.T) {
	parent := todo.New("Plan the trip")
	parent.Project = "house"
	child := todo.NewSubtask("Book flights", parent.ID)
	child.Project = "house"
	grandchild := todo.NewSubtask("Compare fares", child.ID)
	grandchild.Project = "house"

	m := modelWithTasks(t, parent, child, grandchild)
	m.setProject(m.get(parent.ID), "travel", "set project")
	for _, id := range []string{parent.ID, child.ID, grandchild.ID} {
		if got := m.get(id).Project; got != "travel" {
			t.Errorf("%s project = %q, want travel", m.get(id).Title, got)
		}
	}

	m.performUndo()
	for _, id := range []string{parent.ID, child.ID, grandchild.ID} {
		if got := m.get(id).Project; got != "house" {
			t.Errorf("after undo, %s project = %q, want house", m.get(id).Title, got)
		}
	}

	m.setProject(m.get(parent.ID), "", "remove project")
	if got := m.get(grandchild.ID).Project; got != "" {
		t.Errorf("removing the parent's project left the grandchild in %q", got)
	}
}

func TestCLIEditProjectCarriesToSubtasks(t *testing.T) {
	setTestHome(t, t.TempDir())
	if code := cliAdd([]string{"Plan the trip", "--project", "house"}); code != 0 {
		t.Fatalf("add: exit %d", code)
	}
	if code := cliSubtask([]string{"plan", "Book flights"}); code != 0 {
		t.Fatalf("subtask: exit %d", code)
	}
	captureStderr(t, func() {
		if code := cliEdit([]string{"plan", "--project", "travel"}); code != 0 {
			t.Fatalf("edit: exit %d", code)
		}
	})
	_, todos, err := loadForCLI()
	if err != nil {
		t.Fatal(err)
	}
	for _, td := range todos {
		if td.Project != "travel" {
			t.Errorf("%s project = %q, want travel", td.Title, td.Project)
		}
	}
}

// Settings → "Subtasks copy tags" decides whether a new subtask takes the
// parent's tags; project and deadline come along either way.
func TestSubtaskTagsSetting(t *testing.T) {
	m := settingsModel(t)
	m.settingsCursor = settingSubtaskTags
	m = sendKey(t, m, "enter")
	if m.subtaskTags {
		t.Fatal("enter did not switch copying tags off")
	}
	if storedSubtaskTags() {
		t.Error("settings.json still says subtasks copy tags")
	}

	if code := cliAdd([]string{"Plan the trip", "--project", "house", "--tag", "waiting"}); code != 0 {
		t.Fatalf("add: exit %d", code)
	}
	if code := cliSubtask([]string{"plan", "Book flights"}); code != 0 {
		t.Fatalf("subtask: exit %d", code)
	}
	_, todos, err := loadForCLI()
	if err != nil {
		t.Fatal(err)
	}
	for _, td := range todos {
		if td.ParentID == "" {
			continue
		}
		if len(td.Tags) != 0 || td.Project != "house" {
			t.Errorf("with copying off, subtask tags = %v, project = %q; want no tags, project house", td.Tags, td.Project)
		}
	}

	m = sendKey(t, m, "enter")
	if !m.subtaskTags || !storedSubtaskTags() {
		t.Error("enter again did not switch copying tags back on")
	}
	if code := cliSubtask([]string{"plan", "Find a hotel"}); code != 0 {
		t.Fatalf("subtask: exit %d", code)
	}
	_, todos, _ = loadForCLI()
	for _, td := range todos {
		if td.Title == "Find a hotel" && !slices.Contains(td.Tags, "waiting") {
			t.Errorf("with copying on, subtask tags = %v, want the parent's #waiting", td.Tags)
		}
	}
}
