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
type refreshMsg struct {
	issues []*beadslite.Issue
}
