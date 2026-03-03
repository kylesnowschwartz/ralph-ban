package main

import beadslite "github.com/kylesnowschwartz/beads-lite"

// moveMsg signals that a card should move to a different column.
// The board intercepts this and routes it to the target column.
type moveMsg struct {
	card   card
	source columnIndex
	target columnIndex
}

// saveMsg carries a created or edited issue back from the form to the board.
type saveMsg struct {
	issue *beadslite.Issue
}

// deleteMsg requests deletion of a card from the current column.
type deleteMsg struct {
	id string
}

// priorityMsg signals a priority change on the selected card.
type priorityMsg struct {
	card  card
	delta int // -1 = higher priority (toward P0), +1 = lower (toward P4)
}

// errMsg carries an error from async operations (persistence, refresh).
type errMsg struct {
	err error
}

// refreshMsg carries fresh issue data from a periodic SQLite poll.
// blockedIDs is the set of issue IDs that have at least one unresolved blocker —
// i.e. they depend on an issue that is not yet done.
// fingerprint is a hash of the issue set so applyRefresh can detect external changes
// without comparing every field. Only external changes invalidate the undo stack.
type refreshMsg struct {
	issues      []*beadslite.Issue
	blockedIDs  map[string]bool
	fingerprint uint64
}

// closeMsg carries a card closure request from the resolution picker to the board.
// The resolution is chosen by the user before the move to Done is finalized.
type closeMsg struct {
	card       card
	source     columnIndex
	resolution beadslite.Resolution
}

// depLinkMsg carries a dependency link request from the dep-link picker to the board.
// focusedID is the card that was focused when the picker was opened.
// pickedID is the card the user selected from the list.
// mode determines which direction the dependency runs.
type depLinkMsg struct {
	focusedID string
	pickedID  string
	mode      depLinkMode
}
