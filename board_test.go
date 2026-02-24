package main

import (
	"testing"

	"github.com/charmbracelet/bubbles/list"
	beadslite "github.com/kylesnowschwartz/beads-lite"
)

// newTestBoard creates a board with an in-memory store and reasonable
// terminal dimensions so columns are usable in tests.
func newTestBoard(t *testing.T) *board {
	t.Helper()
	store := newTestStore(t)
	b := newBoard(store)
	b.termWidth = 120
	b.termHeight = 40
	b.loaded = true
	b.resizeColumns()
	return b
}

// --- handlePriority boundary clamping ---

func TestHandlePriority_ClampAtP0(t *testing.T) {
	b := newTestBoard(t)

	issue := makeIssue("bl-p0", "Already P0", beadslite.StatusTodo)
	issue.Priority = 0

	b.focused = colTodo
	b.cols[colTodo].Focus()
	b.cols[colTodo].SetItems([]list.Item{card{issue: issue}})

	cmd := b.handlePriority(priorityMsg{
		card:  card{issue: issue},
		delta: -1, // try to go above P0
	})

	if cmd != nil {
		t.Error("handlePriority should return nil at P0 boundary")
	}
	if issue.Priority != 0 {
		t.Errorf("priority = %d, want 0 (should not change)", issue.Priority)
	}
}

func TestHandlePriority_ClampAtP4(t *testing.T) {
	b := newTestBoard(t)

	issue := makeIssue("bl-p4", "Already P4", beadslite.StatusTodo)
	issue.Priority = 4

	b.focused = colTodo
	b.cols[colTodo].Focus()
	b.cols[colTodo].SetItems([]list.Item{card{issue: issue}})

	cmd := b.handlePriority(priorityMsg{
		card:  card{issue: issue},
		delta: 1, // try to go below P4
	})

	if cmd != nil {
		t.Error("handlePriority should return nil at P4 boundary")
	}
	if issue.Priority != 4 {
		t.Errorf("priority = %d, want 4 (should not change)", issue.Priority)
	}
}

func TestHandlePriority_ValidAdjustment(t *testing.T) {
	b := newTestBoard(t)

	issue := makeIssue("bl-p2", "Middle Priority", beadslite.StatusTodo)
	issue.Priority = 2

	b.focused = colTodo
	b.cols[colTodo].Focus()
	b.cols[colTodo].SetItems([]list.Item{card{issue: issue}})

	cmd := b.handlePriority(priorityMsg{
		card:  card{issue: issue},
		delta: -1,
	})

	if cmd == nil {
		t.Fatal("handlePriority should return a persist command for valid adjustment")
	}
	if issue.Priority != 1 {
		t.Errorf("priority = %d, want 1", issue.Priority)
	}
}

func TestHandlePriority_ResortsColumn(t *testing.T) {
	b := newTestBoard(t)

	cardA := makeIssue("bl-a", "Was High", beadslite.StatusTodo)
	cardA.Priority = 0
	cardB := makeIssue("bl-b", "Was Low", beadslite.StatusTodo)
	cardB.Priority = 1

	b.focused = colTodo
	b.cols[colTodo].Focus()
	b.cols[colTodo].SetItems([]list.Item{
		card{issue: cardA},
		card{issue: cardB},
	})

	// Lower cardA from P0 to P2 — should sort after cardB (P1).
	b.handlePriority(priorityMsg{
		card:  card{issue: cardA},
		delta: 2,
	})

	items := b.cols[colTodo].list.Items()
	if len(items) != 2 {
		t.Fatalf("todo has %d items, want 2", len(items))
	}
	first := items[0].(card)
	second := items[1].(card)

	if first.issue.ID != "bl-b" {
		t.Errorf("first card = %q, want bl-b (P1 should sort before P2)", first.issue.ID)
	}
	if second.issue.ID != "bl-a" {
		t.Errorf("second card = %q, want bl-a (P2 should sort after P1)", second.issue.ID)
	}
}

// --- undoLastMove ---

func TestUndoLastMove_ReversesMove(t *testing.T) {
	b := newTestBoard(t)

	issue := makeIssue("bl-undo", "Undo Me", beadslite.StatusDoing)

	// Simulate: card was moved from Todo to Doing.
	// It currently sits in the Doing column.
	b.cols[colDoing].SetItems([]list.Item{card{issue: issue}})
	b.cols[b.focused].Blur()
	b.focused = colDoing
	b.cols[colDoing].Focus()
	b.lastMove = &moveMsg{
		card:   card{issue: issue},
		source: colTodo,
		target: colDoing,
	}

	cmd := b.undoLastMove()

	if cmd == nil {
		t.Fatal("undoLastMove should return a persist command")
	}

	// Card should be back in todo
	todoItems := b.cols[colTodo].list.Items()
	if len(todoItems) != 1 {
		t.Errorf("todo has %d items after undo, want 1", len(todoItems))
	}

	// Card should be removed from doing
	doingItems := b.cols[colDoing].list.Items()
	if len(doingItems) != 0 {
		t.Errorf("doing has %d items after undo, want 0", len(doingItems))
	}

	// Focus should follow back to source column
	if b.focused != colTodo {
		t.Errorf("focused = %d, want %d (colTodo)", b.focused, colTodo)
	}

	// lastMove should be consumed
	if b.lastMove != nil {
		t.Error("lastMove should be nil after undo")
	}
}

func TestUndoLastMove_NilWhenNoHistory(t *testing.T) {
	b := newTestBoard(t)
	b.lastMove = nil

	cmd := b.undoLastMove()

	if cmd != nil {
		t.Error("undoLastMove should return nil when no move to undo")
	}
}
