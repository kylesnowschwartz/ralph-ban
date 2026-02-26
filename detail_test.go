package main

import (
	"strings"
	"testing"

	beadslite "github.com/kylesnowschwartz/beads-lite"
)

// noDeps is a convenience shorthand for tests that don't care about dependencies.
func noDeps() ([]depEntry, []depEntry) {
	return nil, nil
}

func TestNewDetail(t *testing.T) {
	issue := makeIssue("bl-det", "Detail Test", beadslite.StatusDoing)
	d := newDetail(issue, colDoing, nil, nil)

	if d.issue != issue {
		t.Error("newDetail should store the issue pointer")
	}
	if d.columnIndex != colDoing {
		t.Errorf("columnIndex = %d, want %d", d.columnIndex, colDoing)
	}
	if d.width != 0 || d.height != 0 {
		t.Errorf("dimensions should be zero before sizing, got %dx%d", d.width, d.height)
	}
}

func TestDetailView_ContainsIssueFields(t *testing.T) {
	issue := makeIssue("bl-det", "Detail Test Card", beadslite.StatusReview)
	issue.Priority = 1
	issue.Type = beadslite.IssueTypeBug

	blockedBy, blocks := noDeps()
	d := newDetail(issue, colReview, blockedBy, blocks)
	d.width = 80
	d.height = 40
	view := d.View()

	for _, want := range []string{
		"Detail Test Card", // title
		"bl-det",           // ID
		"review",           // status
		"P1",               // priority
		"bug",              // type
	} {
		if !strings.Contains(view, want) {
			t.Errorf("View() missing %q", want)
		}
	}
}

func TestDetailView_NoDescription(t *testing.T) {
	issue := makeIssue("bl-nd", "No Desc", beadslite.StatusTodo)
	issue.Description = ""

	blockedBy, blocks := noDeps()
	d := newDetail(issue, colTodo, blockedBy, blocks)
	d.width = 80
	d.height = 40
	view := d.View()

	if !strings.Contains(view, "(no description)") {
		t.Error("View() should show '(no description)' for empty description")
	}
}

func TestDetailView_WithDescription(t *testing.T) {
	issue := makeIssue("bl-wd", "With Desc", beadslite.StatusTodo)
	issue.Description = "Some detailed description"

	blockedBy, blocks := noDeps()
	d := newDetail(issue, colTodo, blockedBy, blocks)
	d.width = 80
	d.height = 40
	view := d.View()

	if !strings.Contains(view, "Some detailed description") {
		t.Error("View() should render the description text")
	}
	if strings.Contains(view, "(no description)") {
		t.Error("View() should not show placeholder when description exists")
	}
}

func TestDetailView_ShowsAssignedTo(t *testing.T) {
	issue := makeIssue("bl-at", "Assigned Card", beadslite.StatusDoing)
	issue.AssignedTo = "claude"

	blockedBy, blocks := noDeps()
	d := newDetail(issue, colDoing, blockedBy, blocks)
	d.width = 80
	d.height = 40
	view := d.View()

	if !strings.Contains(view, "claude") {
		t.Error("View() should show AssignedTo when set")
	}
}

func TestDetailView_HelpHints(t *testing.T) {
	issue := makeIssue("bl-ht", "Help Hints", beadslite.StatusTodo)

	blockedBy, blocks := noDeps()
	d := newDetail(issue, colTodo, blockedBy, blocks)
	d.width = 80
	d.height = 40
	view := d.View()

	if !strings.Contains(view, "e: edit") {
		t.Error("View() should show edit hint")
	}
	if !strings.Contains(view, "esc: close") {
		t.Error("View() should show close hint")
	}
}

func TestDetailView_NoDeps_NoDepSections(t *testing.T) {
	issue := makeIssue("bl-nd2", "No Deps Card", beadslite.StatusTodo)

	d := newDetail(issue, colTodo, nil, nil)
	d.width = 80
	d.height = 40
	view := d.View()

	if strings.Contains(view, "Blocked by") {
		t.Error("View() should not show 'Blocked by' when there are no blockers")
	}
	if strings.Contains(view, "Blocks") {
		t.Error("View() should not show 'Blocks' when this card blocks nothing")
	}
}

func TestDetailView_BlockedBy(t *testing.T) {
	issue := makeIssue("bl-b1", "Blocked Card", beadslite.StatusTodo)

	blockedBy := []depEntry{
		{id: "bl-blocker", title: "The Blocker"},
	}
	d := newDetail(issue, colTodo, blockedBy, nil)
	d.width = 80
	d.height = 40
	view := d.View()

	if !strings.Contains(view, "Blocked by") {
		t.Error("View() should show 'Blocked by' section")
	}
	if !strings.Contains(view, "bl-blocker") {
		t.Error("View() should show the blocker's ID")
	}
	if !strings.Contains(view, "The Blocker") {
		t.Error("View() should show the blocker's title")
	}
	// Should not show the Blocks section since blocks is nil.
	if strings.Contains(view, "\nBlocks\n") {
		t.Error("View() should not show 'Blocks' section when blocks is nil")
	}
}

func TestDetailView_Blocks(t *testing.T) {
	issue := makeIssue("bl-dep", "Depended-on Card", beadslite.StatusDoing)

	blocks := []depEntry{
		{id: "bl-waiter", title: "Waiting Card"},
	}
	d := newDetail(issue, colDoing, nil, blocks)
	d.width = 80
	d.height = 40
	view := d.View()

	if !strings.Contains(view, "Blocks") {
		t.Error("View() should show 'Blocks' section")
	}
	if !strings.Contains(view, "bl-waiter") {
		t.Error("View() should show the dependent's ID")
	}
	if !strings.Contains(view, "Waiting Card") {
		t.Error("View() should show the dependent's title")
	}
}

func TestDetailView_BothDeps(t *testing.T) {
	issue := makeIssue("bl-mid", "Middle Card", beadslite.StatusDoing)

	blockedBy := []depEntry{
		{id: "bl-above", title: "Above Card"},
	}
	blocks := []depEntry{
		{id: "bl-below", title: "Below Card"},
	}
	d := newDetail(issue, colDoing, blockedBy, blocks)
	d.width = 80
	d.height = 40
	view := d.View()

	if !strings.Contains(view, "Blocked by") {
		t.Error("View() should show 'Blocked by' section")
	}
	if !strings.Contains(view, "bl-above") {
		t.Error("View() should show blocker ID")
	}
	if !strings.Contains(view, "Blocks") {
		t.Error("View() should show 'Blocks' section")
	}
	if !strings.Contains(view, "bl-below") {
		t.Error("View() should show dependent ID")
	}
}

func TestDetailView_MultipleDeps(t *testing.T) {
	issue := makeIssue("bl-multi", "Multi Dep Card", beadslite.StatusTodo)

	blockedBy := []depEntry{
		{id: "bl-b1", title: "First Blocker"},
		{id: "bl-b2", title: "Second Blocker"},
	}
	d := newDetail(issue, colTodo, blockedBy, nil)
	d.width = 80
	d.height = 40
	view := d.View()

	if !strings.Contains(view, "bl-b1") {
		t.Error("View() should show first blocker ID")
	}
	if !strings.Contains(view, "bl-b2") {
		t.Error("View() should show second blocker ID")
	}
	if !strings.Contains(view, "First Blocker") {
		t.Error("View() should show first blocker title")
	}
	if !strings.Contains(view, "Second Blocker") {
		t.Error("View() should show second blocker title")
	}
}
