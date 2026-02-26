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

// undoKind identifies what kind of operation an undoEntry reverses.
type undoKind int

const (
	undoMove     undoKind = iota // reverse a column move
	undoPriority                 // restore a previous priority
	undoEdit                     // restore a previous issue state (title, description, etc.)
	undoDelete                   // re-create a deleted card
)

// undoEntry holds the data needed to reverse a single user action.
// Only the fields relevant to the operation's undoKind are populated.
type undoEntry struct {
	kind undoKind

	// undoMove: the original move message (source/target/card before status change)
	move *moveMsg

	// undoPriority: card ID, column it lives in, and the priority to restore
	priorityCardID string
	priorityCol    columnIndex
	oldPriority    int

	// undoEdit: the full issue state before the edit
	// undoDelete: the full issue state to re-create
	issue *beadslite.Issue
}

const maxUndoStack = 8

// undoStack is a slice of undoEntry values treated as a stack (append to push, pop from end).
// Capacity is capped at maxUndoStack; the oldest entry is dropped when full.
type undoStack []undoEntry

// push appends a new entry, evicting the oldest if the cap is reached.
func (s *undoStack) push(entry undoEntry) {
	if len(*s) >= maxUndoStack {
		// Drop oldest (index 0) to make room.
		copy(*s, (*s)[1:])
		*s = (*s)[:len(*s)-1]
	}
	*s = append(*s, entry)
}

// pop removes and returns the most recent entry.
// Returns false if the stack is empty.
func (s *undoStack) pop() (undoEntry, bool) {
	if len(*s) == 0 {
		return undoEntry{}, false
	}
	top := (*s)[len(*s)-1]
	*s = (*s)[:len(*s)-1]
	return top, true
}

// clear empties the stack (used when external changes make undo entries stale).
func (s *undoStack) clear() {
	*s = (*s)[:0]
}

// computeUndoMove determines the parameters to reverse a move.
// Returns nil if the move message is nil.
func computeUndoMove(lastMove *moveMsg) *moveResult {
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
