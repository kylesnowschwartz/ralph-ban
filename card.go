package main

import (
	"fmt"
	"image/color"

	"charm.land/lipgloss/v2"
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
		lock := lipgloss.NewStyle().Foreground(colorIconLock).Render(iconLock)
		return lock + "  " + base
	}
	return base
}

// issueTypeIcon returns a colored icon for the given issue type.
// Each type gets a distinct color so the glyph communicates type at a glance.
// Icon definitions live in icons.go; colors live in theme.go.
func issueTypeIcon(t beadslite.IssueType) string {
	var icon string
	var fg color.Color

	switch t {
	case beadslite.IssueTypeBug:
		icon, fg = iconBug, colorIconBug
	case beadslite.IssueTypeFeature:
		icon, fg = iconFeature, colorIconFeature
	case beadslite.IssueTypeEpic:
		icon, fg = iconEpic, colorIconEpic
	default: // IssueTypeTask and any unknown types
		icon, fg = iconTask, colorIconTask
	}

	return lipgloss.NewStyle().Foreground(fg).Render(icon)
}
