package main

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/list"
	"charm.land/lipgloss/v2"
	beadslite "github.com/kylesnowschwartz/beads-lite"
)

// filterField names which issue field the active filter applies to.
// An empty string means no filter is active.
type filterField string

const (
	filterNone     filterField = ""
	filterPriority filterField = "priority"
	filterType     filterField = "type"
	filterAssignee filterField = "assignee"
)

// activeFilter holds the current filter state.
// When field is filterNone, no filtering is applied.
type activeFilter struct {
	field filterField
	value string
}

// matches returns true if the given issue passes the filter.
// A filterNone filter always passes.
func (f activeFilter) matches(issue *beadslite.Issue) bool {
	switch f.field {
	case filterNone:
		return true
	case filterPriority:
		return fmt.Sprintf("P%d", issue.Priority) == f.value
	case filterType:
		return string(issue.Type) == f.value
	case filterAssignee:
		return issue.AssignedTo == f.value
	default:
		return true
	}
}

// buildFilterSteps constructs the ordered cycle of filters from the set of issues.
// Cycle order: no filter -> P0 -> P1 -> P2 -> P3 -> P4 -> task -> bug -> feature -> epic
// -> each unique assignee (sorted by first appearance) -> back to no filter.
// Steps with no matching issues are excluded to avoid dead-weight filter states.
func buildFilterSteps(issues []*beadslite.Issue) []activeFilter {
	steps := []activeFilter{{field: filterNone}}

	// Priority filters P0-P4
	for p := 0; p <= 4; p++ {
		val := fmt.Sprintf("P%d", p)
		for _, issue := range issues {
			if fmt.Sprintf("P%d", issue.Priority) == val {
				steps = append(steps, activeFilter{field: filterPriority, value: val})
				break
			}
		}
	}

	// Type filters in a fixed order
	for _, t := range []string{"task", "bug", "feature", "epic"} {
		for _, issue := range issues {
			if string(issue.Type) == t {
				steps = append(steps, activeFilter{field: filterType, value: t})
				break
			}
		}
	}

	// Assignee filters: unique, in order of first appearance
	seen := map[string]bool{}
	for _, issue := range issues {
		if issue.AssignedTo != "" && !seen[issue.AssignedTo] {
			seen[issue.AssignedTo] = true
			steps = append(steps, activeFilter{field: filterAssignee, value: issue.AssignedTo})
		}
	}

	return steps
}

// nextFilter advances to the next filter in the cycle.
// It rebuilds the step list from the current issues so newly assigned cards
// appear as filter targets without requiring a restart.
func nextFilter(current activeFilter, issues []*beadslite.Issue) activeFilter {
	steps := buildFilterSteps(issues)
	if len(steps) <= 1 {
		return activeFilter{field: filterNone}
	}

	// Find the index of the current filter in the step list.
	for i, f := range steps {
		if f.field == current.field && f.value == current.value {
			return steps[(i+1)%len(steps)]
		}
	}

	// Current filter not found in steps (e.g. its data was deleted) — reset to first step after none.
	return steps[1]
}

// prevFilter moves backward in the filter cycle.
func prevFilter(current activeFilter, issues []*beadslite.Issue) activeFilter {
	steps := buildFilterSteps(issues)
	if len(steps) <= 1 {
		return activeFilter{field: filterNone}
	}

	for i, f := range steps {
		if f.field == current.field && f.value == current.value {
			prev := (i - 1 + len(steps)) % len(steps)
			return steps[prev]
		}
	}

	return activeFilter{field: filterNone}
}

// filterStepLabel returns a short display name for a filter step.
// "none" becomes "all", priority/type/assignee values are used as-is.
func filterStepLabel(f activeFilter) string {
	if f.field == filterNone {
		return "all"
	}
	return f.value
}

// filterCycleView renders a compact cycle indicator for the footer.
// It shows up to maxVisible steps centred around the active step, with
// "..." on either side when more steps exist beyond the window.
// The active step is highlighted. Example output:
//
//	filter: … P0 | [P1] | P2 | P3 …   f/F cycle · esc clear
func filterCycleView(current activeFilter, issues []*beadslite.Issue, maxVisible int) string {
	steps := buildFilterSteps(issues)
	if len(steps) == 0 {
		return ""
	}

	// Find the index of the active filter.
	activeIdx := 0
	for i, s := range steps {
		if s.field == current.field && s.value == current.value {
			activeIdx = i
			break
		}
	}

	// Calculate the window of steps to show, centred on activeIdx.
	half := maxVisible / 2
	start := activeIdx - half
	end := activeIdx + (maxVisible - half)
	if start < 0 {
		end -= start // shift right
		start = 0
	}
	if end > len(steps) {
		diff := end - len(steps)
		start -= diff // shift left
		end = len(steps)
		if start < 0 {
			start = 0
		}
	}

	hasLeft := start > 0
	hasRight := end < len(steps)

	activeStyle := lipgloss.NewStyle().
		Foreground(colorWarning).
		Bold(true)
	dimStyle := styleFaint()
	labelStyle := lipgloss.NewStyle().
		Foreground(colorWarning).
		Bold(true)

	var parts []string
	for i := start; i < end; i++ {
		s := steps[i]
		lbl := filterStepLabel(s)
		if i == activeIdx {
			parts = append(parts, activeStyle.Render("["+lbl+"]"))
		} else {
			parts = append(parts, dimStyle.Render(lbl))
		}
	}

	strip := strings.Join(parts, dimStyle.Render(" | "))
	if hasLeft {
		strip = dimStyle.Render("… ") + strip
	}
	if hasRight {
		strip = strip + dimStyle.Render(" …")
	}

	hint := dimStyle.Render("  f/F cycle · esc clear")

	return labelStyle.Render("filter: ") + strip + hint
}

// applyFilterToItems returns only the items that pass the filter.
// An empty result is returned as an empty (non-nil) slice.
func applyFilterToItems(items []list.Item, f activeFilter) []list.Item {
	if f.field == filterNone {
		return items
	}
	var out []list.Item
	for _, item := range items {
		c, ok := item.(card)
		if !ok {
			continue
		}
		if f.matches(c.issue) {
			out = append(out, item)
		}
	}
	if out == nil {
		out = []list.Item{}
	}
	return out
}
