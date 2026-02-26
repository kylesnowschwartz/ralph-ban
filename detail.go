package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	beadslite "github.com/kylesnowschwartz/beads-lite"
)

// depEntry is a resolved dependency: the issue ID and its title.
// Storing both avoids a second store lookup during rendering.
type depEntry struct {
	id    string
	title string
}

// detail is a read-only overlay showing full card information.
// Press e to jump into edit mode, esc to close.
type detail struct {
	issue       *beadslite.Issue
	columnIndex columnIndex
	width       int
	height      int

	// blockedBy lists issues that must complete before this card can proceed.
	// blocks lists issues that are waiting on this card.
	// Both are resolved at construction time so View() is a pure renderer.
	blockedBy []depEntry
	blocks    []depEntry
}

// newDetail constructs a detail overlay. blockedBy and blocks are resolved
// dependency lists: what this issue depends on, and what depends on this issue.
func newDetail(issue *beadslite.Issue, colIdx columnIndex, blockedBy, blocks []depEntry) detail {
	return detail{
		issue:       issue,
		columnIndex: colIdx,
		blockedBy:   blockedBy,
		blocks:      blocks,
	}
}

func (d detail) Update(msg tea.Msg) (detail, tea.Cmd) {
	// Resize, refresh, errMsg, and suspend are handled centrally by
	// board.Update before overlay routing. Key events (esc, e) are
	// handled by board.updateDetail. Nothing reaches here that this
	// model needs to act on, but the signature satisfies the contract
	// for future extension.
	return d, nil
}

func (d detail) View() string {
	i := d.issue

	labelStyle := lipgloss.NewStyle().Bold(true).Width(10)
	faintStyle := lipgloss.NewStyle().Faint(true)

	title := lipgloss.NewStyle().Bold(true).Render(i.Title)

	desc := faintStyle.Render("(no description)")
	if i.Description != "" {
		desc = i.Description
	}

	fields := lipgloss.JoinVertical(lipgloss.Left,
		title,
		"",
		desc,
		"",
		labelStyle.Render("ID")+"  "+i.ID,
		labelStyle.Render("Status")+"  "+string(i.Status),
		labelStyle.Render("Priority")+"  "+fmt.Sprintf("P%d", i.Priority),
		labelStyle.Render("Type")+"  "+string(i.Type),
	)

	if i.AssignedTo != "" {
		fields = lipgloss.JoinVertical(lipgloss.Left,
			fields,
			labelStyle.Render("Assigned")+"  "+i.AssignedTo,
		)
	}

	// Render dependency sections only when entries exist.
	// Each entry shows as "  id  title" so the user can look up blocked cards.
	if len(d.blockedBy) > 0 {
		fields = lipgloss.JoinVertical(lipgloss.Left,
			fields,
			"",
			faintStyle.Render("Blocked by"),
			formatDeps(d.blockedBy),
		)
	}
	if len(d.blocks) > 0 {
		fields = lipgloss.JoinVertical(lipgloss.Left,
			fields,
			"",
			faintStyle.Render("Blocks"),
			formatDeps(d.blocks),
		)
	}

	fields = lipgloss.JoinVertical(lipgloss.Left,
		fields,
		"",
		faintStyle.Render("e: edit  esc: close"),
	)

	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("62")).
		Padding(1, 2).
		Width(60)

	rendered := style.Render(fields)

	return lipgloss.Place(d.width, d.height,
		lipgloss.Center, lipgloss.Center,
		rendered,
	)
}

// formatDeps renders a slice of depEntries as indented lines: "  id  title".
func formatDeps(deps []depEntry) string {
	lines := make([]string, len(deps))
	for idx, dep := range deps {
		lines[idx] = "  " + dep.id + "  " + dep.title
	}
	return strings.Join(lines, "\n")
}
