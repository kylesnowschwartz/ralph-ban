package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	beadslite "github.com/kylesnowschwartz/beads-lite"
)

// writeHeartbeat writes a unix timestamp to a temp heartbeat file.
func writeHeartbeat(t *testing.T, dir, agent string, ts int64) {
	t.Helper()
	err := os.WriteFile(filepath.Join(dir, agent), []byte(fmt.Sprintf("%d", ts)), 0o644)
	if err != nil {
		t.Fatalf("writeHeartbeat: %v", err)
	}
}

func makeActivityIssue(id, title, assignedTo string, status beadslite.Status) *beadslite.Issue {
	return &beadslite.Issue{
		ID:         id,
		Title:      title,
		AssignedTo: assignedTo,
		Status:     status,
	}
}

func TestScanActivity_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	entries := scanActivity(dir, nil)
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for empty dir, got %d", len(entries))
	}
}

func TestScanActivity_MissingDir(t *testing.T) {
	// Non-existent directory should fail open with no entries.
	entries := scanActivity("/nonexistent/path/heartbeats", nil)
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for missing dir, got %d", len(entries))
	}
}

func TestScanActivity_ActiveAgent(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Unix()
	// Heartbeat written 30 seconds ago — should be active.
	writeHeartbeat(t, dir, "worker-1", now-30)

	issue := makeIssue("bl-abc1", "Fix thing", "worker-1", beadslite.StatusDoing)
	entries := scanActivity(dir, []*beadslite.Issue{issue})

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.name != "worker-1" {
		t.Errorf("name: got %q, want %q", e.name, "worker-1")
	}
	if e.status != statusActive {
		t.Errorf("status: got %q, want %q", e.status, statusActive)
	}
	if e.cardID != "bl-abc1" {
		t.Errorf("cardID: got %q, want %q", e.cardID, "bl-abc1")
	}
}

func TestScanActivity_StalledAgent(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Unix()
	// Heartbeat 10 minutes ago — stale threshold is 5 min.
	writeHeartbeat(t, dir, "worker-stalled", now-600)

	issue := makeIssue("bl-xyz", "Old task", "worker-stalled", beadslite.StatusDoing)
	entries := scanActivity(dir, []*beadslite.Issue{issue})

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].status != statusStalled {
		t.Errorf("status: got %q, want %q", entries[0].status, statusStalled)
	}
}

func TestScanActivity_IdleAgentNoHeartbeat(t *testing.T) {
	dir := t.TempDir()
	// No heartbeat file, but agent has a doing card.
	issue := makeIssue("bl-new", "New card", "worker-fresh", beadslite.StatusDoing)
	entries := scanActivity(dir, []*beadslite.Issue{issue})

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.status != statusIdle {
		t.Errorf("status: got %q, want %q", e.status, statusIdle)
	}
	if e.lastSeen != -1 {
		t.Errorf("lastSeen: got %v, want -1", e.lastSeen)
	}
}

func TestScanActivity_HeartbeatWithNoDoingCard(t *testing.T) {
	// Agent wrote a heartbeat but has no doing card — should not appear
	// since completed workers get cleaned up by board-sync.sh. However,
	// if a stale file slips through, we exclude it (no doing card = no entry).
	dir := t.TempDir()
	now := time.Now().Unix()
	writeHeartbeat(t, dir, "ghost-agent", now-30)

	// No issues passed — ghost has no doing card.
	entries := scanActivity(dir, nil)
	// The heartbeat file exists but no doing card means classifyStatus returns idle,
	// and since the agent isn't in doingCards it won't appear at all.
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for agent with no doing card, got %d", len(entries))
	}
}

func TestScanActivity_SortOrder(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Unix()

	// stalled agent
	writeHeartbeat(t, dir, "worker-b", now-600)
	issueB := makeIssue("bl-b", "Task B", "worker-b", beadslite.StatusDoing)

	// active agent
	writeHeartbeat(t, dir, "worker-a", now-30)
	issueA := makeIssue("bl-a", "Task A", "worker-a", beadslite.StatusDoing)

	// idle (no heartbeat)
	issueC := makeIssue("bl-c", "Task C", "worker-c", beadslite.StatusDoing)

	entries := scanActivity(dir, []*beadslite.Issue{issueA, issueB, issueC})

	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	// active should come first
	if entries[0].status != statusActive {
		t.Errorf("entry[0]: got %q, want active", entries[0].status)
	}
	// stalled second
	if entries[1].status != statusStalled {
		t.Errorf("entry[1]: got %q, want stalled", entries[1].status)
	}
	// idle last
	if entries[2].status != statusIdle {
		t.Errorf("entry[2]: got %q, want idle", entries[2].status)
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{45 * time.Second, "45s ago"},
		{3 * time.Minute, "3m ago"},
		{2 * time.Hour, "2h ago"},
		{0, "0s ago"},
		{-1 * time.Second, "—"},
	}
	for _, tc := range tests {
		got := formatDuration(tc.d)
		if got != tc.want {
			t.Errorf("formatDuration(%v): got %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		s    string
		n    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello world", 8, "hello w…"},
		{"hi", 2, "hi"},
		{"abc", 1, "…"},
	}
	for _, tc := range tests {
		got := truncate(tc.s, tc.n)
		if got != tc.want {
			t.Errorf("truncate(%q, %d): got %q, want %q", tc.s, tc.n, got, tc.want)
		}
	}
}

func TestClassifyStatus(t *testing.T) {
	issue := makeIssue("bl-x", "Test", "agent", beadslite.StatusDoing)

	tests := []struct {
		elapsed time.Duration
		card    *beadslite.Issue
		want    agentStatus
	}{
		{30 * time.Second, issue, statusActive},
		{4*time.Minute + 59*time.Second, issue, statusActive}, // just under threshold
		{5 * time.Minute, issue, statusStalled},
		{10 * time.Minute, issue, statusStalled},
		{30 * time.Second, nil, statusIdle}, // no card = idle regardless of heartbeat
	}
	for _, tc := range tests {
		got := classifyStatus(tc.elapsed, tc.card)
		if got != tc.want {
			t.Errorf("classifyStatus(%v, card=%v): got %q, want %q",
				tc.elapsed, tc.card != nil, got, tc.want)
		}
	}
}
