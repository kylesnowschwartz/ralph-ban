package main

import (
	"fmt"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	beadslite "github.com/kylesnowschwartz/beads-lite"
)

// resolutionOption pairs a display label with the beads-lite Resolution value.
type resolutionOption struct {
	label      string
	resolution beadslite.Resolution
}

var resolutionOptions = []resolutionOption{
	{"done", beadslite.ResolutionDone},
	{"wontfix", beadslite.ResolutionWontfix},
	{"duplicate", beadslite.ResolutionDuplicate},
}

// resolutionPicker is a lightweight modal overlay that asks the user to pick
// a resolution before a card is moved into the Done column. It avoids invalid
// state by using a selector (left/right) rather than a text input.
type resolutionPicker struct {
	card   card
	source columnIndex
	index  int // selected option index
	width  int
	height int
}

func newResolutionPicker(cd card, source columnIndex) resolutionPicker {
	return resolutionPicker{
		card:   cd,
		source: source,
		index:  0, // default: done
	}
}

func (r resolutionPicker) Update(msg tea.Msg) (resolutionPicker, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, keys.Left):
			r.index = (r.index - 1 + len(resolutionOptions)) % len(resolutionOptions)
			return r, nil

		case key.Matches(msg, keys.Right):
			r.index = (r.index + 1) % len(resolutionOptions)
			return r, nil

		case msg.Type == tea.KeyEnter:
			chosen := resolutionOptions[r.index].resolution
			c := r.card
			src := r.source
			return r, func() tea.Msg {
				return closeMsg{card: c, source: src, resolution: chosen}
			}

		case key.Matches(msg, keys.Back):
			// Esc is handled by board.updateResolution to cancel and restore view.
			return r, nil
		}
	}
	return r, nil
}

func (r resolutionPicker) View() string {
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("62")).
		Padding(1, 2).
		Width(44)

	active := lipgloss.NewStyle().Foreground(lipgloss.Color("170")).Bold(true)
	faint := lipgloss.NewStyle().Faint(true)
	label := lipgloss.NewStyle().Width(12)

	header := lipgloss.NewStyle().Bold(true).Render("Close Card")

	cardTitle := faint.Render(r.card.issue.Title)
	if len(r.card.issue.Title) > 38 {
		cardTitle = faint.Render(r.card.issue.Title[:35] + "...")
	}

	opt := resolutionOptions[r.index]
	selectorValue := fmt.Sprintf("◀ %s ▶", opt.label)

	resLabel := label.Render("Resolution:")
	resRow := resLabel + " " + active.Render(selectorValue)

	hint := faint.Render("←/→: pick  enter: confirm  esc: cancel")

	content := lipgloss.JoinVertical(lipgloss.Left,
		header,
		"",
		cardTitle,
		"",
		resRow,
		"",
		hint,
	)

	rendered := style.Render(content)

	return lipgloss.Place(r.width, r.height,
		lipgloss.Center, lipgloss.Center,
		rendered,
	)
}
