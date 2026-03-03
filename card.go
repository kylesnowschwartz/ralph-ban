package main

import (
	"fmt"

	beadslite "github.com/kylesnowschwartz/beads-lite"
)

// card wraps a beads-lite Issue for display in a bubbles/list.
// Implements the list.Item and list.DefaultItem interfaces.
type card struct {
	issue   *beadslite.Issue
	blocked bool // true when this card has at least one unresolved blocker
}

func (c card) Title() string       { return c.issue.Title }
func (c card) FilterValue() string { return c.issue.Title + " " + c.issue.Description }

// Description shows priority, type icon, ID, and assignee (if claimed) on the second line.
// Blocked cards get a lock icon prefix so the constraint is visible without opening the detail.
// The dimming of blocked cards is handled separately by ageAwareDelegate.Render.
func (c card) Description() string {
	typeIcon := issueTypeIcon(c.issue.Type)
	base := fmt.Sprintf("%s P%d · %s", typeIcon, c.issue.Priority, c.issue.ID)
	if c.issue.AssignedTo != "" {
		base += " @" + c.issue.AssignedTo
	}
	if c.blocked {
		return "󰌾  " + base
	}
	return base
}

// issueTypeIcon returns a nerd font icon for the given issue type.
// These condense type information into a single glyph so the description
// line stays compact without spelling out "task", "bug", etc.
//   - 󰄬  (nf-md-check_circle_outline) task
//   - 󰃤  (nf-md-bug)                  bug
//   - 󰙴  (nf-md-star_circle)           feature
//   - 󱈸  (nf-md-layers_triple)         epic
func issueTypeIcon(t beadslite.IssueType) string {
	switch t {
	case beadslite.IssueTypeBug:
		return "󰃤"
	case beadslite.IssueTypeFeature:
		return "󰙴"
	case beadslite.IssueTypeEpic:
		return "󱈸"
	default: // IssueTypeTask and any unknown types
		return "󰄬"
	}
}
