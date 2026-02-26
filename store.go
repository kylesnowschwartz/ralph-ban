package main

import (
	"sort"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	beadslite "github.com/kylesnowschwartz/beads-lite"
)

const refreshInterval = 2 * time.Second

// loadIssues fetches all issues from the store and partitions them into
// per-column item slices. The board calls this on init and on each refresh tick.
func loadIssues(store *beadslite.Store) ([numColumns][]list.Item, error) {
	issues, err := store.ListIssues()
	if err != nil {
		return [numColumns][]list.Item{}, err
	}
	return partitionByStatus(issues), nil
}

// partitionByStatus sorts issues into column buckets by status,
// with cards sorted by priority within each column (P0 first).
func partitionByStatus(issues []*beadslite.Issue) [numColumns][]list.Item {
	var buckets [numColumns][]list.Item
	for _, issue := range issues {
		col, ok := statusToColumn[issue.Status]
		if !ok {
			continue // skip unknown statuses
		}
		buckets[col] = append(buckets[col], card{issue: issue})
	}
	for i := range buckets {
		sort.Slice(buckets[i], func(a, b int) bool {
			ca := buckets[i][a].(card)
			cb := buckets[i][b].(card)
			return ca.issue.Priority < cb.issue.Priority
		})
	}
	return buckets
}

// persistMove updates an issue's status in the database after a column move.
func persistMove(store *beadslite.Store, id string, target columnIndex) tea.Cmd {
	return func() tea.Msg {
		issue, err := store.GetIssue(id)
		if err != nil {
			return errMsg{err}
		}
		issue.Status = columnToStatus[target]
		if err := store.UpdateIssue(issue); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

// persistDelete removes an issue from the database.
func persistDelete(store *beadslite.Store, id string) tea.Cmd {
	return func() tea.Msg {
		if err := store.DeleteIssue(id); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

// persistCreate inserts a new issue into the database.
func persistCreate(store *beadslite.Store, issue *beadslite.Issue) tea.Cmd {
	return func() tea.Msg {
		if err := store.CreateIssue(issue); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

// persistUpdate saves an edited issue to the database.
func persistUpdate(store *beadslite.Store, issue *beadslite.Issue) tea.Cmd {
	return func() tea.Msg {
		if err := store.UpdateIssue(issue); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

// persistClose closes an issue in the database with the given resolution.
// This calls CloseIssue which sets status=done, clears assigned_to, and sets closed_at.
func persistClose(store *beadslite.Store, id string, resolution beadslite.Resolution) tea.Cmd {
	return func() tea.Msg {
		if err := store.CloseIssue(id, resolution); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

// tickRefresh starts the polling loop that reloads from SQLite every refreshInterval.
func tickRefresh(store *beadslite.Store) tea.Cmd {
	return tea.Tick(refreshInterval, func(time.Time) tea.Msg {
		issues, err := store.ListIssues()
		if err != nil {
			return errMsg{err}
		}
		return refreshMsg{issues: issues}
	})
}
