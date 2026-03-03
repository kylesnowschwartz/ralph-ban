package main

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	beadslite "github.com/kylesnowschwartz/beads-lite"
)

// depLinkMode distinguishes which direction the dependency runs.
// In blockedBy mode, the focused card will depend on the picked card.
// In blocks mode, the picked card will depend on the focused card.
type depLinkMode int

const (
	depModeBlockedBy depLinkMode = iota // focused card is blocked by selection
	depModeBlocks                       // focused card blocks selection
)

// pickerItem wraps a card as a list.Item for the dep-link picker.
// It shows the card ID and title so the user can identify cards at a glance.
type pickerItem struct {
	id    string
	title string
}

func (p pickerItem) Title() string       { return p.id + "  " + p.title }
func (p pickerItem) Description() string { return "" }
func (p pickerItem) FilterValue() string { return p.id + " " + p.title }

// depLinker is a modal overlay for picking a card to link as a dependency.
// It reuses bubbles/list for built-in keyboard navigation and type-to-filter.
type depLinker struct {
	list      list.Model
	focusedID string // ID of the card that will have the dep attached
	mode      depLinkMode
	width     int
	height    int
}

// newDepLinker builds a picker pre-populated with all issues except the focused card.
// issues must be the full flat slice of all issues from the board cache.
func newDepLinker(issues []*beadslite.Issue, focusedID string, mode depLinkMode) depLinker {
	var items []list.Item
	for _, issue := range issues {
		if issue.ID == focusedID {
			continue // can't link a card to itself
		}
		// Skip done cards — linking to a closed card is rarely useful.
		if issue.Status == beadslite.StatusDone {
			continue
		}
		items = append(items, pickerItem{id: issue.ID, title: issue.Title})
	}

	delegate := list.NewDefaultDelegate()
	delegate.SetHeight(1) // single-line items: ID + title only
	// Disable the description line so items take one row each,
	// fitting more cards in a compact picker.
	delegate.ShowDescription = false
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.
		Foreground(colorAccent).
		BorderLeftForeground(colorAccent)

	l := list.New(items, delegate, 50, 20)
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.DisableQuitKeybindings()

	modeLabel := modeTitle(mode)
	l.Title = modeLabel

	return depLinker{
		list:      l,
		focusedID: focusedID,
		mode:      mode,
	}
}

func modeTitle(mode depLinkMode) string {
	switch mode {
	case depModeBlockedBy:
		return "Blocked by which card?"
	case depModeBlocks:
		return "Blocks which card?"
	default:
		return "Link dependency"
	}
}

func (d depLinker) Update(msg tea.Msg) (depLinker, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// esc is handled by board.updateDepLink to cancel.
		if key.Matches(msg, keys.Back) {
			return d, nil
		}

		if msg.Key().Code == tea.KeyEnter {
			item := d.list.SelectedItem()
			if item == nil {
				return d, nil
			}
			picked, ok := item.(pickerItem)
			if !ok {
				return d, nil
			}
			focusedID := d.focusedID
			mode := d.mode
			return d, func() tea.Msg {
				return depLinkMsg{
					focusedID: focusedID,
					pickedID:  picked.id,
					mode:      mode,
				}
			}
		}
	}

	var cmd tea.Cmd
	d.list, cmd = d.list.Update(msg)
	return d, cmd
}

func (d depLinker) View() string {
	listWidth := min(60, d.width-4)
	listHeight := min(20, d.height-6)
	d.list.SetSize(listWidth-4, listHeight-4)

	// Hint line under the list.
	faint := styleFaint()
	directionHint := depDirectionHint(d.focusedID, d.mode, d.list.SelectedItem())
	hint := faint.Render("↑/↓: nav  type: filter  enter: link  esc: cancel")

	inner := lipgloss.JoinVertical(lipgloss.Left,
		d.list.View(),
		"",
		directionHint,
		hint,
	)

	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		Padding(0, 1).
		Width(listWidth)

	rendered := style.Render(inner)

	return lipgloss.Place(d.width, d.height,
		lipgloss.Center, lipgloss.Center,
		rendered,
	)
}

// depDirectionHint shows a one-liner previewing the dependency that will be created.
func depDirectionHint(focusedID string, mode depLinkMode, item list.Item) string {
	style := lipgloss.NewStyle().Foreground(colorAccent)

	picked := "(none selected)"
	if item != nil {
		if p, ok := item.(pickerItem); ok {
			picked = p.id
		}
	}

	var preview string
	switch mode {
	case depModeBlockedBy:
		preview = fmt.Sprintf("%s  is blocked by  %s", focusedID, picked)
	case depModeBlocks:
		preview = fmt.Sprintf("%s  blocks  %s", focusedID, picked)
	}

	return style.Render(strings.TrimSpace(preview))
}
