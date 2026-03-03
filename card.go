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
		return iconLock + "  " + base
	}
	return base
}

// issueTypeIcon returns the icon constant for the given issue type.
// These condense type information into a single glyph so the description
// line stays compact without spelling out "task", "bug", etc.
// Icon definitions live in icons.go.
func issueTypeIcon(t beadslite.IssueType) string {
	switch t {
	case beadslite.IssueTypeBug:
		return iconBug
	case beadslite.IssueTypeFeature:
		return iconFeature
	case beadslite.IssueTypeEpic:
		return iconEpic
	default: // IssueTypeTask and any unknown types
		return iconTask
	}
}
