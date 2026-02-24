package main

import (
	"strings"
	"testing"

	beadslite "github.com/kylesnowschwartz/beads-lite"
)

func TestNewDetail(t *testing.T) {
	issue := makeIssue("bl-det", "Detail Test", beadslite.StatusDoing)
	d := newDetail(issue, colDoing)

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

	d := newDetail(issue, colReview)
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

	d := newDetail(issue, colTodo)
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

	d := newDetail(issue, colTodo)
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

	d := newDetail(issue, colDoing)
	d.width = 80
	d.height = 40
	view := d.View()

	if !strings.Contains(view, "claude") {
		t.Error("View() should show AssignedTo when set")
	}
}

func TestDetailView_HelpHints(t *testing.T) {
	issue := makeIssue("bl-ht", "Help Hints", beadslite.StatusTodo)

	d := newDetail(issue, colTodo)
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
