package main

import (
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

func TestComputeUndo_NilWhenNoHistory(t *testing.T) {
	if computeUndo(nil) != nil {
		t.Error("expected nil when no lastMove")
	}
}

func TestComputeUndo_ReversesSourceAndTarget(t *testing.T) {
	last := &moveMsg{
		card:   card{issue: &beadslite.Issue{ID: "bl-undo", Status: beadslite.StatusDoing}},
		source: colTodo,
		target: colDoing,
	}
	result := computeUndo(last)
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
