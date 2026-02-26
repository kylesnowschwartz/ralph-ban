package main

import (
	"fmt"

	beadslite "github.com/kylesnowschwartz/beads-lite"
)

// card wraps a beads-lite Issue for display in a bubbles/list.
// Implements the list.Item and list.DefaultItem interfaces.
type card struct {
	issue *beadslite.Issue
}

func (c card) Title() string       { return c.issue.Title }
func (c card) FilterValue() string { return c.issue.Title }

// Description shows priority, type, ID, and assignee (if claimed) on the second line of each card.
func (c card) Description() string {
	base := fmt.Sprintf("P%d %s · %s", c.issue.Priority, c.issue.Type, c.issue.ID)
	if c.issue.AssignedTo == "" {
		return base
	}
	return base + " @" + c.issue.AssignedTo
}
