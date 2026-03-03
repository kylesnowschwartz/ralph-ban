package main

import (
	"testing"

	beadslite "github.com/kylesnowschwartz/beads-lite"
)

func TestCardImplementsListItem(t *testing.T) {
	issue := &beadslite.Issue{
		ID:       "bl-test",
		Title:    "Test Card",
		Priority: 1,
		Type:     beadslite.IssueTypeBug,
	}
	c := card{issue: issue}

	if c.Title() != "Test Card" {
		t.Errorf("Title() = %q, want %q", c.Title(), "Test Card")
	}
	if want := "󰃤 P1 · bl-test"; c.Description() != want {
		t.Errorf("Description() = %q, want %q", c.Description(), want)
	}
	// FilterValue includes title + description so search matches both.
	if want := "Test Card "; c.FilterValue() != want {
		t.Errorf("FilterValue() = %q, want %q", c.FilterValue(), want)
	}
}

func TestCardFilterValueIncludesDescription(t *testing.T) {
	issue := &beadslite.Issue{
		ID:          "bl-test",
		Title:       "Fix login",
		Description: "Token refresh fails after 30 minutes",
	}
	c := card{issue: issue}
	fv := c.FilterValue()
	if fv != "Fix login Token refresh fails after 30 minutes" {
		t.Errorf("FilterValue() = %q, want title + space + description", fv)
	}
}

func TestCardDescriptionShowsAssignee(t *testing.T) {
	issue := &beadslite.Issue{
		ID:         "bl-test",
		Title:      "Claimed Card",
		Priority:   2,
		Type:       beadslite.IssueTypeTask,
		AssignedTo: "worker-assignee",
	}
	c := card{issue: issue}

	want := "󰄬 P2 · bl-test @worker-assignee"
	if c.Description() != want {
		t.Errorf("Description() = %q, want %q", c.Description(), want)
	}
}

func TestCardDescriptionNoAssignee(t *testing.T) {
	issue := &beadslite.Issue{
		ID:       "bl-test",
		Title:    "Unclaimed Card",
		Priority: 3,
		Type:     beadslite.IssueTypeFeature,
	}
	c := card{issue: issue}

	want := "󰙴 P3 · bl-test"
	if c.Description() != want {
		t.Errorf("Description() = %q, want %q", c.Description(), want)
	}
}

func TestCardDescriptionBlockedShowsLockIcon(t *testing.T) {
	issue := &beadslite.Issue{
		ID:       "bl-test",
		Title:    "Blocked Card",
		Priority: 2,
		Type:     beadslite.IssueTypeTask,
	}
	c := card{issue: issue, blocked: true}

	want := "󰌾  󰄬 P2 · bl-test"
	if c.Description() != want {
		t.Errorf("Description() = %q, want %q", c.Description(), want)
	}
}

func TestCardDescriptionEpicIcon(t *testing.T) {
	issue := &beadslite.Issue{
		ID:       "bl-epic",
		Title:    "Epic Card",
		Priority: 0,
		Type:     beadslite.IssueTypeEpic,
	}
	c := card{issue: issue}

	want := "󱈸 P0 · bl-epic"
	if c.Description() != want {
		t.Errorf("Description() = %q, want %q", c.Description(), want)
	}
}

func TestIssueTypeIcon(t *testing.T) {
	tests := []struct {
		issueType beadslite.IssueType
		wantIcon  string
	}{
		{beadslite.IssueTypeTask, "󰄬"},
		{beadslite.IssueTypeBug, "󰃤"},
		{beadslite.IssueTypeFeature, "󰙴"},
		{beadslite.IssueTypeEpic, "󱈸"},
	}
	for _, tt := range tests {
		got := issueTypeIcon(tt.issueType)
		if got != tt.wantIcon {
			t.Errorf("issueTypeIcon(%q) = %q, want %q", tt.issueType, got, tt.wantIcon)
		}
	}
}
