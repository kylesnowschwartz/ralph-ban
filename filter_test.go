package main

import (
	"testing"

	"github.com/charmbracelet/bubbles/list"
	beadslite "github.com/kylesnowschwartz/beads-lite"
)

func makeFilterIssue(id string, priority int, issueType beadslite.IssueType, assignedTo string) *beadslite.Issue {
	return &beadslite.Issue{
		ID:         id,
		Title:      "Test " + id,
		Priority:   priority,
		Type:       issueType,
		AssignedTo: assignedTo,
	}
}

func TestActiveFilterLabel(t *testing.T) {
	tests := []struct {
		filter activeFilter
		want   string
	}{
		{activeFilter{field: filterNone}, "none"},
		{activeFilter{field: filterPriority, value: "P0"}, "priority=P0"},
		{activeFilter{field: filterType, value: "bug"}, "type=bug"},
		{activeFilter{field: filterAssignee, value: "alice"}, "assignee=alice"},
	}
	for _, tt := range tests {
		got := tt.filter.label()
		if got != tt.want {
			t.Errorf("label() = %q, want %q", got, tt.want)
		}
	}
}

func TestActiveFilterMatches(t *testing.T) {
	issue := makeFilterIssue("x1", 2, beadslite.IssueTypeBug, "alice")

	tests := []struct {
		name   string
		filter activeFilter
		want   bool
	}{
		{"no filter passes everything", activeFilter{field: filterNone}, true},
		{"matching priority", activeFilter{field: filterPriority, value: "P2"}, true},
		{"non-matching priority", activeFilter{field: filterPriority, value: "P0"}, false},
		{"matching type", activeFilter{field: filterType, value: "bug"}, true},
		{"non-matching type", activeFilter{field: filterType, value: "task"}, false},
		{"matching assignee", activeFilter{field: filterAssignee, value: "alice"}, true},
		{"non-matching assignee", activeFilter{field: filterAssignee, value: "bob"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.filter.matches(issue)
			if got != tt.want {
				t.Errorf("matches() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNextFilterCycles(t *testing.T) {
	issues := []*beadslite.Issue{
		makeFilterIssue("a", 1, beadslite.IssueTypeTask, "alice"),
		makeFilterIssue("b", 2, beadslite.IssueTypeBug, ""),
	}

	// Start at no filter, cycle forward until we return to no filter.
	start := activeFilter{field: filterNone}
	f := start
	visited := map[string]int{}
	maxSteps := 20

	for i := 0; i < maxSteps; i++ {
		f = nextFilter(f, issues)
		label := f.label()
		visited[label]++
		if visited[label] > 1 {
			t.Fatalf("cycle revisited %q at step %d", label, i)
		}
		if f.field == filterNone {
			// Completed one full cycle
			return
		}
	}
	t.Fatalf("did not return to no-filter after %d steps", maxSteps)
}

func TestNextFilterSkipsMissingData(t *testing.T) {
	// No P0 issues — P0 filter step should not appear.
	issues := []*beadslite.Issue{
		makeFilterIssue("a", 3, beadslite.IssueTypeTask, ""),
	}
	steps := buildFilterSteps(issues)
	for _, s := range steps {
		if s.field == filterPriority && s.value == "P0" {
			t.Errorf("P0 filter should not appear when no P0 issues exist")
		}
	}
}

func TestPrevFilter(t *testing.T) {
	issues := []*beadslite.Issue{
		makeFilterIssue("a", 1, beadslite.IssueTypeTask, ""),
	}
	steps := buildFilterSteps(issues)
	if len(steps) < 2 {
		t.Skip("not enough steps to test prevFilter")
	}

	// Going next then prev should return to the start.
	start := activeFilter{field: filterNone}
	next := nextFilter(start, issues)
	back := prevFilter(next, issues)
	if back.field != start.field || back.value != start.value {
		t.Errorf("prevFilter after nextFilter = %v, want %v", back, start)
	}

	// Same for second step.
	second := nextFilter(next, issues)
	backToNext := prevFilter(second, issues)
	if backToNext.field != next.field || backToNext.value != next.value {
		t.Errorf("prevFilter from second step = %v, want %v", backToNext, next)
	}
}

func TestApplyFilterToItems(t *testing.T) {
	p1bug := card{issue: makeFilterIssue("a", 1, beadslite.IssueTypeBug, "")}
	p2task := card{issue: makeFilterIssue("b", 2, beadslite.IssueTypeTask, "alice")}
	items := []list.Item{p1bug, p2task}

	t.Run("no filter returns all", func(t *testing.T) {
		out := applyFilterToItems(items, activeFilter{field: filterNone})
		if len(out) != 2 {
			t.Errorf("got %d items, want 2", len(out))
		}
	})

	t.Run("priority filter", func(t *testing.T) {
		out := applyFilterToItems(items, activeFilter{field: filterPriority, value: "P1"})
		if len(out) != 1 {
			t.Errorf("got %d items, want 1", len(out))
		}
	})

	t.Run("type filter", func(t *testing.T) {
		out := applyFilterToItems(items, activeFilter{field: filterType, value: "bug"})
		if len(out) != 1 {
			t.Errorf("got %d items, want 1", len(out))
		}
	})

	t.Run("assignee filter", func(t *testing.T) {
		out := applyFilterToItems(items, activeFilter{field: filterAssignee, value: "alice"})
		if len(out) != 1 {
			t.Errorf("got %d items, want 1", len(out))
		}
	})

	t.Run("no match returns empty slice", func(t *testing.T) {
		out := applyFilterToItems(items, activeFilter{field: filterAssignee, value: "nobody"})
		if out == nil {
			t.Error("got nil, want empty slice")
		}
		if len(out) != 0 {
			t.Errorf("got %d items, want 0", len(out))
		}
	})
}
