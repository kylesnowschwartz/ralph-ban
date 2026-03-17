package main

import (
	"fmt"
	"hash/fnv"
	"sort"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	beadslite "github.com/kylesnowschwartz/beads-lite"
)

const refreshInterval = 2 * time.Second

// partitionByStatus sorts issues into column buckets by status,
// with cards sorted by priority within each column (P0 first).
// blockedIDs marks which issues have unresolved blockers; pass nil to skip.
func partitionByStatus(issues []*beadslite.Issue, blockedIDs map[string]bool) [numColumns][]list.Item {
	var buckets [numColumns][]list.Item
	for _, issue := range issues {
		col, ok := statusToColumn[issue.Status]
		if !ok {
			continue // skip unknown statuses
		}
		buckets[col] = append(buckets[col], card{
			issue:   issue,
			blocked: blockedIDs[issue.ID],
		})
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

// persistAddDep adds a blocks-type dependency link between two issues.
// issueID depends on dependsOnID (i.e. dependsOnID blocks issueID).
func persistAddDep(store *beadslite.Store, issueID, dependsOnID string) tea.Cmd {
	return func() tea.Msg {
		if err := store.AddDependency(issueID, dependsOnID, beadslite.DepBlocks); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

// persistClose closes an issue in the database with the given resolution.
// This calls CloseIssue which sets status=done, clears assigned_to, and sets closed_at.
func persistClose(store *beadslite.Store, id string, resolution beadslite.Resolution) tea.Cmd {
	return func() tea.Msg {
		if _, err := store.CloseIssue(id, resolution); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

// tickRefresh starts the polling loop that reloads from SQLite every refreshInterval.
// Each tick fetches issues and dependencies in two queries, then computes which
// issues have at least one unresolved blocker (a blocker that is not done).
func tickRefresh(store *beadslite.Store) tea.Cmd {
	return tea.Tick(refreshInterval, func(time.Time) tea.Msg {
		return fetchRefresh(store)
	})
}

// fetchRefresh executes the two queries needed for a full refresh and returns
// a refreshMsg. Factored out so board.loadFromStore can reuse the same logic.
func fetchRefresh(store *beadslite.Store) tea.Msg {
	issues, err := store.ListIssues()
	if err != nil {
		return errMsg{err}
	}
	blockedIDs := computeBlockedIDs(store, issues)
	return refreshMsg{
		issues:      issues,
		blockedIDs:  blockedIDs,
		fingerprint: issueFingerprint(issues),
	}
}

// issueFingerprint computes a fast hash over the mutable fields of an issue set.
// Used to detect external changes between poll ticks — if the fingerprint hasn't
// changed, no external write occurred and the undo stack is safe to keep.
func issueFingerprint(issues []*beadslite.Issue) uint64 {
	h := fnv.New64a()
	for _, iss := range issues {
		// ID + Status + Priority + UpdatedAt covers all fields that affect
		// board rendering. Title/Description changes also update UpdatedAt.
		fmt.Fprintf(h, "%s:%s:%d:%d\n",
			iss.ID, iss.Status, iss.Priority, iss.UpdatedAt.UnixNano())
	}
	return h.Sum64()
}

// computeBlockedIDs returns the set of issue IDs that have at least one
// unresolved blocks-type dependency (i.e. the blocker issue is not done).
// A single call to GetAllDependencies gives us everything needed without N+1 queries.
func computeBlockedIDs(store *beadslite.Store, issues []*beadslite.Issue) map[string]bool {
	allDeps, err := store.GetAllDependencies()
	if err != nil {
		// Fail open: if we can't read deps, show no cards as blocked.
		return nil
	}

	// Build a status index so blocker resolution is O(1).
	statusOf := make(map[string]beadslite.Status, len(issues))
	for _, issue := range issues {
		statusOf[issue.ID] = issue.Status
	}

	blocked := make(map[string]bool)
	for issueID, deps := range allDeps {
		for _, dep := range deps {
			if dep.Type != beadslite.DepBlocks {
				continue
			}
			blockerStatus, known := statusOf[dep.DependsOnID]
			if !known {
				// Dangling reference — treat as unresolved to be conservative.
				blocked[issueID] = true
				continue
			}
			if blockerStatus != beadslite.StatusDone {
				blocked[issueID] = true
			}
		}
	}
	return blocked
}
