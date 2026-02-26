package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	beadslite "github.com/kylesnowschwartz/beads-lite"
)

// agentStatus classifies how recently an agent checked in.
type agentStatus string

const (
	statusActive  agentStatus = "active"  // heartbeat < 5 min old
	statusStalled agentStatus = "stalled" // heartbeat >= 5 min old, but has doing card
	statusIdle    agentStatus = "idle"    // no doing card
)

// heartbeatStaleSeconds is the threshold beyond which an agent is stalled.
// Matches the shell constant HEARTBEAT_STALE_SECONDS in heartbeat.sh.
const heartbeatStaleSeconds = 300

// agentEntry is one row in the activity table.
type agentEntry struct {
	name       string
	cardID     string // empty when idle
	cardTitle  string
	status     agentStatus
	lastSeen   time.Duration // time since last heartbeat; -1 when no file
}

// activityRefreshMsg carries fresh agent data from a scan of the heartbeats dir.
type activityRefreshMsg struct {
	entries []agentEntry
}

// activity is a read-only overlay showing active agent liveness data.
// It is driven by the same 2-second tick as the board — no separate timer needed.
type activity struct {
	entries     []agentEntry
	width       int
	height      int
	heartbeatDir string
}

// newActivity constructs the activity view.
// heartbeatDir is the path to .ralph-ban/heartbeats/.
func newActivity(heartbeatDir string) activity {
	return activity{heartbeatDir: heartbeatDir}
}

// Init returns a command that loads the initial activity data.
func (a activity) Init() tea.Cmd {
	return nil // populated on first refresh tick from board
}

// Update handles activity view messages.
// Key events (esc, a) are handled by board.Update before reaching here.
func (a activity) Update(msg tea.Msg) (activity, tea.Cmd) {
	switch msg := msg.(type) {
	case activityRefreshMsg:
		a.entries = msg.entries
	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
	}
	return a, nil
}

// View renders the agent activity table.
func (a activity) View() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Underline(true)
	headerStyle := lipgloss.NewStyle().Faint(true).Bold(true)
	rulerStyle := lipgloss.NewStyle().Faint(true)
	hintStyle := lipgloss.NewStyle().Faint(true)

	activeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("42"))   // green
	stalledStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214")) // orange
	idleStyle := lipgloss.NewStyle().Faint(true)

	lines := []string{
		titleStyle.Render("Agent Activity"),
		rulerStyle.Render(strings.Repeat("─", 60)),
	}

	if len(a.entries) == 0 {
		lines = append(lines, hintStyle.Render("No active agents found."))
		lines = append(lines, "", hintStyle.Render("Agents write heartbeats to .ralph-ban/heartbeats/"))
		lines = append(lines, "", hintStyle.Render("a / esc: back to board"))
		return lipgloss.Place(a.width, a.height, lipgloss.Left, lipgloss.Top,
			lipgloss.JoinVertical(lipgloss.Left, lines...))
	}

	// Header row
	lines = append(lines, fmt.Sprintf("%-20s  %-10s  %-8s  %s",
		headerStyle.Render("agent"),
		headerStyle.Render("card"),
		headerStyle.Render("status"),
		headerStyle.Render("last seen"),
	))

	for _, e := range a.entries {
		var statusStr string
		var statusStyled string
		switch e.status {
		case statusActive:
			statusStr = "active"
			statusStyled = activeStyle.Render(statusStr)
		case statusStalled:
			statusStr = "stalled"
			statusStyled = stalledStyle.Render(statusStr)
		case statusIdle:
			statusStr = "idle"
			statusStyled = idleStyle.Render(statusStr)
		}

		cardField := "—"
		if e.cardID != "" {
			cardField = e.cardID
		}

		lastSeenField := "—"
		if e.lastSeen >= 0 {
			lastSeenField = formatDuration(e.lastSeen)
		}

		line := fmt.Sprintf("%-20s  %-10s  %-8s  %s",
			truncate(e.name, 20),
			truncate(cardField, 10),
			statusStyled,
			lastSeenField,
		)
		lines = append(lines, line)
	}

	lines = append(lines, "", hintStyle.Render("a / esc: back to board"))

	return lipgloss.Place(a.width, a.height, lipgloss.Left, lipgloss.Top,
		lipgloss.JoinVertical(lipgloss.Left, lines...))
}

// scanActivity reads the heartbeats directory and bl issue list to produce
// one agentEntry per known agent. Issues are pre-fetched so this function
// can run inside a tea.Cmd goroutine without calling the store directly.
// Pass nil for issues to skip card matching (e.g. in tests).
func scanActivity(heartbeatDir string, issues []*beadslite.Issue) []agentEntry {
	now := time.Now().Unix()

	// Build a map from assigned_to -> doing card for O(1) lookup.
	doingCards := make(map[string]*beadslite.Issue)
	for _, issue := range issues {
		if issue.Status == beadslite.StatusDoing && issue.AssignedTo != "" {
			// Keep the first doing card per agent — agents should only have one.
			if _, exists := doingCards[issue.AssignedTo]; !exists {
				doingCards[issue.AssignedTo] = issue
			}
		}
	}

	// Track which agents we have seen in heartbeat files.
	seen := make(map[string]bool)
	var entries []agentEntry

	// Walk heartbeat files. Missing dir → no heartbeat entries.
	infos, err := os.ReadDir(heartbeatDir)
	if err == nil {
		for _, info := range infos {
			if info.IsDir() {
				continue
			}
			agentName := info.Name()
			seen[agentName] = true

			raw, readErr := os.ReadFile(filepath.Join(heartbeatDir, agentName))
			if readErr != nil {
				continue
			}
			ts, parseErr := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
			if parseErr != nil {
				continue
			}

			elapsed := time.Duration(now-ts) * time.Second
			cardIssue := doingCards[agentName]
			status := classifyStatus(elapsed, cardIssue)
			entry := agentEntry{
				name:     agentName,
				lastSeen: elapsed,
				status:   status,
			}
			if cardIssue != nil {
				entry.cardID = cardIssue.ID
				entry.cardTitle = cardIssue.Title
			}
			entries = append(entries, entry)
		}
	}

	// Agents with doing cards but no heartbeat file show as idle (they may
	// have just started and haven't written a heartbeat yet).
	for agentName, issue := range doingCards {
		if !seen[agentName] {
			entries = append(entries, agentEntry{
				name:      agentName,
				cardID:    issue.ID,
				cardTitle: issue.Title,
				status:    statusIdle,
				lastSeen:  -1,
			})
		}
	}

	// Stable sort: active first, then stalled, then idle; alphabetical within group.
	sort.Slice(entries, func(i, j int) bool {
		si := statusRank(entries[i].status)
		sj := statusRank(entries[j].status)
		if si != sj {
			return si < sj
		}
		return entries[i].name < entries[j].name
	})

	return entries
}

// classifyStatus determines the agent's status from elapsed time and card ownership.
func classifyStatus(elapsed time.Duration, doingCard *beadslite.Issue) agentStatus {
	if doingCard == nil {
		return statusIdle
	}
	if elapsed >= heartbeatStaleSeconds*time.Second {
		return statusStalled
	}
	return statusActive
}

// statusRank maps status to a sort priority (lower = first).
func statusRank(s agentStatus) int {
	switch s {
	case statusActive:
		return 0
	case statusStalled:
		return 1
	default:
		return 2
	}
}

// formatDuration renders a duration as a short human-readable string.
// Examples: "42s ago", "3m ago", "2h ago".
func formatDuration(d time.Duration) string {
	if d < 0 {
		return "—"
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}

// truncate shortens s to at most n runes, appending "…" if truncated.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}
