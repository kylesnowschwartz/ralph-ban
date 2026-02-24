package main

import beadslite "github.com/kylesnowschwartz/beads-lite"

// moveResult describes the outcome of a card move, computed without side effects.
type moveResult struct {
	cardID    string
	newStatus beadslite.Status
	source    columnIndex
	target    columnIndex
}

// computeMove determines the result of moving a card between columns.
// Returns nil if the move is invalid (target out of bounds).
func computeMove(cd card, source, target columnIndex) *moveResult {
	if target < 0 || target >= numColumns {
		return nil
	}
	return &moveResult{
		cardID:    cd.issue.ID,
		newStatus: columnToStatus[target],
		source:    source,
		target:    target,
	}
}

// computePriority determines the new priority after adjustment.
// Returns -1 if the change would exceed bounds [0, 4].
func computePriority(current, delta int) int {
	next := current + delta
	if next < 0 || next > 4 {
		return -1
	}
	return next
}

// computeUndo determines the parameters to reverse a move.
// Returns nil if there's nothing to undo.
func computeUndo(lastMove *moveMsg) *moveResult {
	if lastMove == nil {
		return nil
	}
	return &moveResult{
		cardID:    lastMove.card.issue.ID,
		newStatus: columnToStatus[lastMove.source],
		source:    lastMove.target,
		target:    lastMove.source,
	}
}
