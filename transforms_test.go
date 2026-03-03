package main

import (
	"fmt"
	"testing"

	beadslite "github.com/kylesnowschwartz/beads-lite"
)

func TestComputeMove_Valid(t *testing.T) {
	cd := card{issue: &beadslite.Issue{ID: "bl-1", Status: beadslite.StatusTodo}}
	result := computeMove(cd, colTodo, colDoing)
	if result == nil {
		t.Fatal("expected non-nil result for valid move")
	}
	if result.cardID != "bl-1" {
		t.Errorf("cardID = %q, want bl-1", result.cardID)
	}
	if result.newStatus != beadslite.StatusDoing {
		t.Errorf("newStatus = %q, want %q", result.newStatus, beadslite.StatusDoing)
	}
	if result.source != colTodo || result.target != colDoing {
		t.Errorf("source/target = %d/%d, want %d/%d", result.source, result.target, colTodo, colDoing)
	}
}

func TestComputeMove_SameColumn(t *testing.T) {
	cd := card{issue: &beadslite.Issue{ID: "bl-1", Status: beadslite.StatusTodo}}
	result := computeMove(cd, colTodo, colTodo)
	if result == nil {
		t.Fatal("same-column move should return a result (caller decides whether to act on it)")
	}
	if result.source != result.target {
		t.Errorf("source = %d, target = %d, expected equal for same-column move", result.source, result.target)
	}
}

func TestComputeMove_OutOfBounds(t *testing.T) {
	cd := card{issue: &beadslite.Issue{ID: "bl-1"}}
	if computeMove(cd, colDone, colDone+1) != nil {
		t.Error("expected nil for target beyond last column")
	}
	if computeMove(cd, colBacklog, -1) != nil {
		t.Error("expected nil for negative target")
	}
}

func TestComputePriority_Valid(t *testing.T) {
	if got := computePriority(2, -1); got != 1 {
		t.Errorf("computePriority(2, -1) = %d, want 1", got)
	}
	if got := computePriority(2, 1); got != 3 {
		t.Errorf("computePriority(2, 1) = %d, want 3", got)
	}
}

func TestComputePriority_Bounds(t *testing.T) {
	if got := computePriority(0, -1); got != -1 {
		t.Errorf("computePriority(0, -1) = %d, want -1 (below floor)", got)
	}
	if got := computePriority(4, 1); got != -1 {
		t.Errorf("computePriority(4, 1) = %d, want -1 (above ceiling)", got)
	}
}

func TestComputePriority_AtBoundaries(t *testing.T) {
	if got := computePriority(0, 1); got != 1 {
		t.Errorf("computePriority(0, 1) = %d, want 1", got)
	}
	if got := computePriority(4, -1); got != 3 {
		t.Errorf("computePriority(4, -1) = %d, want 3", got)
	}
}

func TestComputeUndoMove_NilWhenNoHistory(t *testing.T) {
	if computeUndoMove(nil) != nil {
		t.Error("expected nil when no lastMove")
	}
}

func TestComputeUndoMove_ReversesSourceAndTarget(t *testing.T) {
	last := &moveMsg{
		card:   card{issue: &beadslite.Issue{ID: "bl-undo", Status: beadslite.StatusDoing}},
		source: colTodo,
		target: colDoing,
	}
	result := computeUndoMove(last)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.source != colDoing {
		t.Errorf("undo source = %d, want %d (original target)", result.source, colDoing)
	}
	if result.target != colTodo {
		t.Errorf("undo target = %d, want %d (original source)", result.target, colTodo)
	}
	if result.newStatus != beadslite.StatusTodo {
		t.Errorf("newStatus = %q, want %q", result.newStatus, beadslite.StatusTodo)
	}
}

// --- undoStack ---

func TestUndoStack_PushAndPop(t *testing.T) {
	var s undoStack
	s.push(undoEntry{kind: undoMove})
	s.push(undoEntry{kind: undoPriority})

	e, ok := s.pop()
	if !ok {
		t.Fatal("pop should return true with entries on the stack")
	}
	if e.kind != undoPriority {
		t.Errorf("pop returned kind %d, want undoPriority (%d)", e.kind, undoPriority)
	}

	e, ok = s.pop()
	if !ok {
		t.Fatal("second pop should return true")
	}
	if e.kind != undoMove {
		t.Errorf("pop returned kind %d, want undoMove (%d)", e.kind, undoMove)
	}

	_, ok = s.pop()
	if ok {
		t.Error("pop on empty stack should return false")
	}
}

func TestUndoStack_EvictsOldestWhenFull(t *testing.T) {
	var s undoStack
	// Fill beyond the cap
	for i := 0; i < maxUndoStack+3; i++ {
		s.push(undoEntry{kind: undoEdit, priorityCardID: fmt.Sprintf("entry-%d", i)})
	}

	if len(s) != maxUndoStack {
		t.Errorf("stack len = %d, want %d (cap)", len(s), maxUndoStack)
	}

	// Pop everything; the earliest entries should be the ones past index 2
	// (the first 3 were evicted as we exceeded the cap).
	entries := make([]undoEntry, 0, maxUndoStack)
	for {
		e, ok := s.pop()
		if !ok {
			break
		}
		entries = append(entries, e)
	}

	// Entries come out newest-first; the oldest remaining should be entry-3.
	oldest := entries[len(entries)-1]
	if oldest.priorityCardID != "entry-3" {
		t.Errorf("oldest remaining entry = %q, want entry-3 (first 3 should be evicted)", oldest.priorityCardID)
	}
}

func TestUndoStack_Clear(t *testing.T) {
	var s undoStack
	s.push(undoEntry{kind: undoMove})
	s.push(undoEntry{kind: undoDelete})
	s.clear()

	if len(s) != 0 {
		t.Errorf("stack len = %d after clear, want 0", len(s))
	}
	_, ok := s.pop()
	if ok {
		t.Error("pop after clear should return false")
	}
}
