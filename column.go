package main

import (
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	beadslite "github.com/kylesnowschwartz/beads-lite"
)

// columnIndex identifies a column by position in the board array.
type columnIndex int

const (
	colBacklog columnIndex = iota
	colTodo
	colDoing
	colReview
	colDone
	numColumns
)

// columnTitles maps column indices to display names.
var columnTitles = [numColumns]string{
	colBacklog: "Backlog",
	colTodo:    "To Do",
	colDoing:   "Doing",
	colReview:  "Review",
	colDone:    "Done",
}

// columnToStatus maps column position to beads-lite status.
var columnToStatus = [numColumns]beadslite.Status{
	colBacklog: beadslite.StatusBacklog,
	colTodo:    beadslite.StatusTodo,
	colDoing:   beadslite.StatusDoing,
	colReview:  beadslite.StatusReview,
	colDone:    beadslite.StatusDone,
}

// statusToColumn maps beads-lite status to column position.
var statusToColumn = map[beadslite.Status]columnIndex{
	beadslite.StatusBacklog: colBacklog,
	beadslite.StatusTodo:    colTodo,
	beadslite.StatusDoing:   colDoing,
	beadslite.StatusReview:  colReview,
	beadslite.StatusDone:    colDone,
}

// column wraps a bubbles/list.Model to display cards in one kanban column.
type column struct {
	index         columnIndex
	list          list.Model
	focus         bool
	confirmDelete bool
	height        int
	width         int
}

func newColumn(idx columnIndex) column {
	// Start with blurred delegate so unfocused columns never show
	// selection highlights. The board calls Focus() on column 0.
	delegate := newBlurredDelegate()
	l := list.New([]list.Item{}, delegate, 0, 0)
	l.Title = columnTitles[idx]
	l.SetShowHelp(false)
	l.SetFilteringEnabled(false)
	l.SetShowStatusBar(false)
	l.DisableQuitKeybindings()

	return column{
		index: idx,
		list:  l,
	}
}

func (c *column) Focus() {
	c.focus = true
	c.list.SetDelegate(newFocusedDelegate())
}

func (c *column) Blur() {
	c.focus = false
	c.confirmDelete = false
	c.list.SetDelegate(newBlurredDelegate())
}

func (c *column) Focused() bool { return c.focus }

// SetSize updates the column's dimensions and passes them to the inner list.
func (c *column) SetSize(w, h int) {
	c.width = w
	c.height = h
	c.list.SetSize(w-2, h-2) // account for border padding
}

// SetItems replaces all items in the column's list.
func (c *column) SetItems(items []list.Item) {
	c.list.SetItems(items)
}

// SelectedCard returns the currently highlighted card, if any.
func (c *column) SelectedCard() (card, bool) {
	item := c.list.SelectedItem()
	if item == nil {
		return card{}, false
	}
	cd, ok := item.(card)
	return cd, ok
}

// Update handles input for this column.
func (c *column) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if !c.focus {
			return nil
		}

		// Confirm-delete: second d confirms, any other key cancels.
		if c.confirmDelete {
			c.confirmDelete = false
			if key.Matches(msg, keys.Delete) {
				return c.deleteCurrent()
			}
			// Fall through — handle the key normally.
		}

		switch {
		case key.Matches(msg, keys.MoveRight):
			return c.moveRight()
		case key.Matches(msg, keys.MoveLeft):
			return c.moveLeft()
		case key.Matches(msg, keys.Delete):
			c.confirmDelete = true
			return nil
		case key.Matches(msg, keys.PriorityUp):
			return c.adjustPriority(-1)
		case key.Matches(msg, keys.PriorityDn):
			return c.adjustPriority(1)
		}
	}
	c.list, cmd = c.list.Update(msg)
	return cmd
}

// View renders the column with a border that reflects focus state.
func (c *column) View() string {
	if c.confirmDelete {
		saved := c.list.Title
		c.list.Title = "Delete? d/esc"
		view := c.getStyle().Render(c.list.View())
		c.list.Title = saved
		return view
	}
	return c.getStyle().Render(c.list.View())
}

// moveRight validates and emits a moveMsg to the next column.
// The actual list mutation happens in board.handleMove for atomicity.
func (c *column) moveRight() tea.Cmd {
	cd, ok := c.SelectedCard()
	if !ok {
		return nil
	}

	target := c.index + 1
	if target >= numColumns {
		return nil
	}

	return func() tea.Msg {
		return moveMsg{card: cd, source: c.index, target: target}
	}
}

// moveLeft validates and emits a moveMsg to the previous column.
// The actual list mutation happens in board.handleMove for atomicity.
func (c *column) moveLeft() tea.Cmd {
	cd, ok := c.SelectedCard()
	if !ok {
		return nil
	}

	if c.index <= 0 {
		return nil
	}
	target := c.index - 1

	return func() tea.Msg {
		return moveMsg{card: cd, source: c.index, target: target}
	}
}

// adjustPriority emits a priorityMsg for the selected card.
func (c *column) adjustPriority(delta int) tea.Cmd {
	cd, ok := c.SelectedCard()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		return priorityMsg{card: cd, delta: delta}
	}
}

// deleteCurrent removes the selected card and emits a deleteMsg.
func (c *column) deleteCurrent() tea.Cmd {
	cd, ok := c.SelectedCard()
	if !ok {
		return nil
	}

	idx := c.list.Index()
	c.list.RemoveItem(idx)

	return func() tea.Msg {
		return deleteMsg{id: cd.issue.ID}
	}
}

// Styling

var (
	focusedBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62")).
			Padding(0, 1)

	blurredBorder = lipgloss.NewStyle().
			Border(lipgloss.HiddenBorder()).
			Padding(0, 1)
)

func (c *column) getStyle() lipgloss.Style {
	if c.focus {
		return focusedBorder.
			Width(c.width).
			Height(c.height)
	}
	return blurredBorder.
		Width(c.width).
		Height(c.height)
}

// Delegate styling for focused vs blurred columns

func newFocusedDelegate() list.DefaultDelegate {
	d := list.NewDefaultDelegate()
	d.Styles.SelectedTitle = d.Styles.SelectedTitle.
		Foreground(lipgloss.Color("170")).
		BorderLeftForeground(lipgloss.Color("170"))
	d.Styles.SelectedDesc = d.Styles.SelectedDesc.
		Foreground(lipgloss.Color("170")).
		BorderLeftForeground(lipgloss.Color("170"))
	return d
}

func newBlurredDelegate() list.DefaultDelegate {
	d := list.NewDefaultDelegate()
	d.Styles.SelectedTitle = d.Styles.NormalTitle
	d.Styles.SelectedDesc = d.Styles.NormalDesc
	return d
}
