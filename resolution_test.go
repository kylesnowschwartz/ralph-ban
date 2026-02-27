package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	beadslite "github.com/kylesnowschwartz/beads-lite"
)

// makeResolutionCard builds a minimal card for use in resolution picker tests.
func makeResolutionCard(id, title string) card {
	return card{
		issue: &beadslite.Issue{
			ID:    id,
			Title: title,
			Type:  beadslite.IssueTypeTask,
		},
	}
}

func TestNewResolutionPicker_DefaultIndex(t *testing.T) {
	cd := makeResolutionCard("bl-001", "Some task")
	r := newResolutionPicker(cd, colDoing)

	if r.index != 0 {
		t.Errorf("default index = %d, want 0 (done)", r.index)
	}
	if r.card.issue.ID != "bl-001" {
		t.Errorf("card ID = %q, want bl-001", r.card.issue.ID)
	}
	if r.source != colDoing {
		t.Errorf("source = %d, want colDoing", r.source)
	}
}

func TestResolutionPicker_RightCycles(t *testing.T) {
	cd := makeResolutionCard("bl-002", "A card")
	r := newResolutionPicker(cd, colTodo)

	// Start at 0 (done). Step right through all options and back to start.
	for i := 0; i < len(resolutionOptions); i++ {
		if r.index != i {
			t.Errorf("before right press %d: index = %d, want %d", i, r.index, i)
		}
		r, _ = r.Update(tea.KeyMsg{Type: tea.KeyRight})
	}

	// After len(resolutionOptions) right presses we should be back at 0.
	if r.index != 0 {
		t.Errorf("after full cycle right: index = %d, want 0 (wrapped)", r.index)
	}
}

func TestResolutionPicker_LeftWrapsFromZero(t *testing.T) {
	cd := makeResolutionCard("bl-003", "A card")
	r := newResolutionPicker(cd, colTodo)

	// Press left from index 0 — should jump to the last option.
	r, _ = r.Update(tea.KeyMsg{Type: tea.KeyLeft})
	want := len(resolutionOptions) - 1
	if r.index != want {
		t.Errorf("left from 0: index = %d, want %d (last option)", r.index, want)
	}
}

func TestResolutionPicker_LeftCycles(t *testing.T) {
	cd := makeResolutionCard("bl-004", "A card")
	r := newResolutionPicker(cd, colTodo)

	// Step right to the last option, then press left back to 0.
	for i := 0; i < len(resolutionOptions)-1; i++ {
		r, _ = r.Update(tea.KeyMsg{Type: tea.KeyRight})
	}
	if r.index != len(resolutionOptions)-1 {
		t.Fatalf("setup failed: expected index at last option, got %d", r.index)
	}

	r, _ = r.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if r.index != len(resolutionOptions)-2 {
		t.Errorf("left from last: index = %d, want %d", r.index, len(resolutionOptions)-2)
	}
}

func TestResolutionPicker_EnterEmitsCloseMsg(t *testing.T) {
	cd := makeResolutionCard("bl-005", "Close me")
	r := newResolutionPicker(cd, colReview)

	// Advance to wontfix (index 1).
	r, _ = r.Update(tea.KeyMsg{Type: tea.KeyRight})
	if r.index != 1 {
		t.Fatalf("expected index 1 after right, got %d", r.index)
	}

	_, cmd := r.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter should return a cmd, got nil")
	}

	msg := cmd()
	cm, ok := msg.(closeMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want closeMsg", msg)
	}
	if cm.card.issue.ID != "bl-005" {
		t.Errorf("closeMsg.card.ID = %q, want bl-005", cm.card.issue.ID)
	}
	if cm.source != colReview {
		t.Errorf("closeMsg.source = %d, want colReview", cm.source)
	}
	if cm.resolution != beadslite.ResolutionWontfix {
		t.Errorf("closeMsg.resolution = %q, want wontfix", cm.resolution)
	}
}

func TestResolutionPicker_EnterWithDoneResolution(t *testing.T) {
	cd := makeResolutionCard("bl-006", "Done card")
	r := newResolutionPicker(cd, colDoing)

	// Default index is 0 (done) — press enter immediately.
	_, cmd := r.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter should return a cmd, got nil")
	}

	msg := cmd()
	cm, ok := msg.(closeMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want closeMsg", msg)
	}
	if cm.resolution != beadslite.ResolutionDone {
		t.Errorf("resolution = %q, want done", cm.resolution)
	}
}

func TestResolutionPicker_EnterWithDuplicateResolution(t *testing.T) {
	cd := makeResolutionCard("bl-007", "Dup card")
	r := newResolutionPicker(cd, colTodo)

	// Advance to duplicate (index 2).
	r, _ = r.Update(tea.KeyMsg{Type: tea.KeyRight})
	r, _ = r.Update(tea.KeyMsg{Type: tea.KeyRight})
	if r.index != 2 {
		t.Fatalf("expected index 2, got %d", r.index)
	}

	_, cmd := r.Update(tea.KeyMsg{Type: tea.KeyEnter})
	msg := cmd()
	cm := msg.(closeMsg)
	if cm.resolution != beadslite.ResolutionDuplicate {
		t.Errorf("resolution = %q, want duplicate", cm.resolution)
	}
}

func TestResolutionPicker_EscReturnsNilCmd(t *testing.T) {
	cd := makeResolutionCard("bl-008", "A card")
	r := newResolutionPicker(cd, colTodo)

	_, cmd := r.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Errorf("esc should return nil cmd, got non-nil")
	}
}

func TestResolutionPicker_ViewContainsExpectedText(t *testing.T) {
	cd := makeResolutionCard("bl-009", "My important task")
	r := newResolutionPicker(cd, colTodo)
	r.width = 80
	r.height = 24

	view := r.View()

	// Header
	if !strings.Contains(view, "Close Card") {
		t.Error("View should contain 'Close Card'")
	}
	// Card title
	if !strings.Contains(view, "My important task") {
		t.Error("View should contain the card title")
	}
	// Current option label (done at index 0)
	if !strings.Contains(view, "done") {
		t.Error("View should contain 'done' (default option label)")
	}
	// Hint text
	if !strings.Contains(view, "enter") {
		t.Error("View should contain hint text with 'enter'")
	}
	if !strings.Contains(view, "esc") {
		t.Error("View should contain hint text with 'esc'")
	}
}

func TestResolutionPicker_ViewTruncatesLongTitle(t *testing.T) {
	longTitle := strings.Repeat("a", 40) // exceeds the 38-char threshold
	cd := makeResolutionCard("bl-010", longTitle)
	r := newResolutionPicker(cd, colTodo)
	r.width = 80
	r.height = 24

	view := r.View()

	// Should be truncated with ellipsis rather than showing the full 40-char title.
	if strings.Contains(view, longTitle) {
		t.Error("View should truncate long titles, not show them in full")
	}
	if !strings.Contains(view, "...") {
		t.Error("View should use '...' to indicate truncation")
	}
}

func TestResolutionPicker_AllOptionsReachable(t *testing.T) {
	cd := makeResolutionCard("bl-011", "Round trip")
	r := newResolutionPicker(cd, colTodo)

	seen := make(map[beadslite.Resolution]bool)
	for i := 0; i < len(resolutionOptions); i++ {
		opt := resolutionOptions[r.index]
		seen[opt.resolution] = true
		r, _ = r.Update(tea.KeyMsg{Type: tea.KeyRight})
	}

	for _, opt := range resolutionOptions {
		if !seen[opt.resolution] {
			t.Errorf("resolution %q was never reachable via right cycling", opt.resolution)
		}
	}
}
