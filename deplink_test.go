package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"

	beadslite "github.com/kylesnowschwartz/beads-lite"
)

// listItems extracts the pickerItem list from a depLinker for inspection.
func listItems(d depLinker) []pickerItem {
	var out []pickerItem
	for _, item := range d.list.Items() {
		if p, ok := item.(pickerItem); ok {
			out = append(out, p)
		}
	}
	return out
}

// --- newDepLinker filtering ---

func TestNewDepLinker_ExcludesSelf(t *testing.T) {
	issues := []*beadslite.Issue{
		makeIssue("bl-001", "Alpha", beadslite.StatusTodo),
		makeIssue("bl-002", "Beta", beadslite.StatusTodo),
		makeIssue("bl-003", "Gamma", beadslite.StatusTodo),
	}

	d := newDepLinker(issues, "bl-002", depModeBlockedBy)
	items := listItems(d)

	for _, item := range items {
		if item.id == "bl-002" {
			t.Error("focused card bl-002 should not appear in picker items")
		}
	}
}

func TestNewDepLinker_ExcludesDoneCards(t *testing.T) {
	issues := []*beadslite.Issue{
		makeIssue("bl-001", "Active", beadslite.StatusTodo),
		makeIssue("bl-002", "Done card", beadslite.StatusDone),
		makeIssue("bl-003", "Also active", beadslite.StatusDoing),
	}

	d := newDepLinker(issues, "bl-999", depModeBlocks)
	items := listItems(d)

	for _, item := range items {
		if item.id == "bl-002" {
			t.Error("done card bl-002 should not appear in picker items")
		}
	}
}

func TestNewDepLinker_IncludesOtherActiveCards(t *testing.T) {
	issues := []*beadslite.Issue{
		makeIssue("bl-001", "Alpha", beadslite.StatusTodo),
		makeIssue("bl-002", "Beta", beadslite.StatusDoing),
		makeIssue("bl-003", "Gamma", beadslite.StatusReview),
		makeIssue("bl-004", "Delta", beadslite.StatusBacklog),
		makeIssue("bl-005", "Focused", beadslite.StatusTodo),
	}

	d := newDepLinker(issues, "bl-005", depModeBlockedBy)
	items := listItems(d)

	// bl-001, bl-002, bl-003, bl-004 should all be present; bl-005 excluded as focused
	wantCount := 4
	if len(items) != wantCount {
		t.Errorf("item count = %d, want %d", len(items), wantCount)
	}

	found := make(map[string]bool)
	for _, item := range items {
		found[item.id] = true
	}
	for _, want := range []string{"bl-001", "bl-002", "bl-003", "bl-004"} {
		if !found[want] {
			t.Errorf("expected %q in picker items but it was missing", want)
		}
	}
}

func TestNewDepLinker_EmptyIssues(t *testing.T) {
	d := newDepLinker(nil, "bl-001", depModeBlockedBy)
	items := listItems(d)

	if len(items) != 0 {
		t.Errorf("empty issues: expected 0 items, got %d", len(items))
	}
}

func TestNewDepLinker_OnlySelfAndDone(t *testing.T) {
	// After filtering self and done, nothing should remain.
	issues := []*beadslite.Issue{
		makeIssue("bl-001", "Self", beadslite.StatusTodo),
		makeIssue("bl-002", "Done", beadslite.StatusDone),
	}

	d := newDepLinker(issues, "bl-001", depModeBlocks)
	items := listItems(d)

	if len(items) != 0 {
		t.Errorf("expected 0 items when only self and done exist, got %d", len(items))
	}
}

// --- modeTitle ---

func TestModeTitle_BlockedBy(t *testing.T) {
	got := modeTitle(depModeBlockedBy)
	if got != "Blocked by which card?" {
		t.Errorf("modeTitle(depModeBlockedBy) = %q, want %q", got, "Blocked by which card?")
	}
}

func TestModeTitle_Blocks(t *testing.T) {
	got := modeTitle(depModeBlocks)
	if got != "Blocks which card?" {
		t.Errorf("modeTitle(depModeBlocks) = %q, want %q", got, "Blocks which card?")
	}
}

func TestModeTitle_Default(t *testing.T) {
	// Any value outside the defined constants hits the default branch.
	got := modeTitle(depLinkMode(99))
	if got != "Link dependency" {
		t.Errorf("modeTitle(99) = %q, want %q", got, "Link dependency")
	}
}

// --- depDirectionHint ---

func TestDepDirectionHint_NilItem(t *testing.T) {
	hint := depDirectionHint("bl-001", depModeBlockedBy, nil)
	// lipgloss may wrap it in ANSI codes; use plain text check.
	// The unrendered string contains "(none selected)".
	if !strings.Contains(hint, "(none selected)") {
		t.Errorf("nil item hint should contain '(none selected)', got %q", hint)
	}
}

func TestDepDirectionHint_BlockedByMode(t *testing.T) {
	item := pickerItem{id: "bl-002", title: "Beta"}
	hint := depDirectionHint("bl-001", depModeBlockedBy, item)

	if !strings.Contains(hint, "bl-001") {
		t.Errorf("blockedBy hint should contain focused ID 'bl-001', got %q", hint)
	}
	if !strings.Contains(hint, "bl-002") {
		t.Errorf("blockedBy hint should contain picked ID 'bl-002', got %q", hint)
	}
	if !strings.Contains(hint, "is blocked by") {
		t.Errorf("blockedBy hint should contain 'is blocked by', got %q", hint)
	}
}

func TestDepDirectionHint_BlocksMode(t *testing.T) {
	item := pickerItem{id: "bl-003", title: "Gamma"}
	hint := depDirectionHint("bl-001", depModeBlocks, item)

	if !strings.Contains(hint, "bl-001") {
		t.Errorf("blocks hint should contain focused ID 'bl-001', got %q", hint)
	}
	if !strings.Contains(hint, "bl-003") {
		t.Errorf("blocks hint should contain picked ID 'bl-003', got %q", hint)
	}
	if !strings.Contains(hint, "blocks") {
		t.Errorf("blocks hint should contain 'blocks', got %q", hint)
	}
}

// --- pickerItem interface ---

func TestPickerItem_Title(t *testing.T) {
	p := pickerItem{id: "bl-042", title: "Some card"}
	want := "bl-042  Some card"
	if p.Title() != want {
		t.Errorf("Title() = %q, want %q", p.Title(), want)
	}
}

func TestPickerItem_Description(t *testing.T) {
	p := pickerItem{id: "bl-042", title: "Some card"}
	if p.Description() != "" {
		t.Errorf("Description() = %q, want empty string", p.Description())
	}
}

func TestPickerItem_FilterValue(t *testing.T) {
	p := pickerItem{id: "bl-042", title: "Some card"}
	want := "bl-042 Some card"
	if p.FilterValue() != want {
		t.Errorf("FilterValue() = %q, want %q", p.FilterValue(), want)
	}
}

func TestPickerItem_ImplementsListItem(t *testing.T) {
	// Compile-time interface check via assignment to list.Item.
	var _ list.Item = pickerItem{id: "bl-001", title: "test"}
}

// --- depLinker mode label on construction ---

func TestNewDepLinker_ListTitleMatchesMode(t *testing.T) {
	issues := []*beadslite.Issue{
		makeIssue("bl-001", "Alpha", beadslite.StatusTodo),
	}

	dBlockedBy := newDepLinker(issues, "bl-999", depModeBlockedBy)
	if dBlockedBy.list.Title != "Blocked by which card?" {
		t.Errorf("list title = %q, want 'Blocked by which card?'", dBlockedBy.list.Title)
	}

	dBlocks := newDepLinker(issues, "bl-999", depModeBlocks)
	if dBlocks.list.Title != "Blocks which card?" {
		t.Errorf("list title = %q, want 'Blocks which card?'", dBlocks.list.Title)
	}
}

func TestNewDepLinker_FocusedIDStored(t *testing.T) {
	issues := []*beadslite.Issue{
		makeIssue("bl-001", "Alpha", beadslite.StatusTodo),
	}

	d := newDepLinker(issues, "bl-001", depModeBlockedBy)
	if d.focusedID != "bl-001" {
		t.Errorf("focusedID = %q, want bl-001", d.focusedID)
	}
}

func TestNewDepLinker_ModeStored(t *testing.T) {
	d := newDepLinker(nil, "bl-001", depModeBlocks)
	if d.mode != depModeBlocks {
		t.Errorf("mode = %d, want depModeBlocks", d.mode)
	}
}
