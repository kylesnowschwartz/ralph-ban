package main

import (
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	beadslite "github.com/kylesnowschwartz/beads-lite"
)

// boardView controls which overlay (if any) is active.
type boardView int

const (
	viewBoard  boardView = iota // default: show columns
	viewForm                    // create/edit form overlay
	viewDetail                  // card detail overlay
	viewSearch                  // cross-column search mode
)

// board is the root tea.Model for the kanban TUI.
type board struct {
	store    *beadslite.Store
	cols     [numColumns]column
	focused  columnIndex
	help     help.Model
	loaded   bool
	quitting bool
	err      error

	// Overlay state: view controls routing; form/detail hold overlay data.
	view   boardView
	form   *form
	detail *detail

	// Single-level undo for accidental moves.
	lastMove *moveMsg

	// Search state: input holds the query; allItems caches the full per-column
	// lists so we can restore them when search is cancelled.
	searchInput textinput.Model
	allItems    [numColumns][]list.Item

	// Layout panning
	termWidth  int
	termHeight int
	panOffset  int // index of first visible column
}

func newBoard(store *beadslite.Store) *board {
	var cols [numColumns]column
	for i := columnIndex(0); i < numColumns; i++ {
		cols[i] = newColumn(i)
	}

	h := help.New()
	// Add visual separation between key and description in help bar.
	// Without this, "n new" reads as one word.
	h.Styles.ShortKey = h.Styles.ShortKey.Bold(true).PaddingRight(1)
	h.Styles.FullKey = h.Styles.FullKey.Bold(true).PaddingRight(1)

	si := textinput.New()
	si.Placeholder = "search cards..."
	si.CharLimit = 80

	b := &board{
		store:       store,
		cols:        cols,
		help:        h,
		searchInput: si,
	}
	b.cols[b.focused].Focus()
	return b
}

func (b *board) Init() tea.Cmd {
	return tea.Batch(
		b.loadFromStore(),
		tickRefresh(b.store),
	)
}

func (b *board) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Cross-cutting messages handled before overlay routing.
	// This prevents errMsg, refreshMsg, and resize from being silently
	// dropped when a form or detail overlay is active.
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		b.termWidth = msg.Width
		b.termHeight = msg.Height
		b.help.Width = msg.Width
		b.loaded = true
		if b.form != nil {
			b.form.width = msg.Width
			b.form.height = msg.Height
		}
		if b.detail != nil {
			b.detail.width = msg.Width
			b.detail.height = msg.Height
		}
		b.updatePan()
		b.resizeColumns()
		return b, nil

	case refreshMsg:
		b.err = nil
		b.applyRefresh(msg.issues)
		return b, tickRefresh(b.store)

	case errMsg:
		b.err = msg.err
		return b, nil

	case tea.ResumeMsg:
		// Returning from ctrl+z suspend. Re-fire a refresh so the
		// board picks up any changes made while backgrounded.
		return b, b.loadFromStore()

	case tea.KeyMsg:
		if key.Matches(msg, keys.Suspend) {
			return b, tea.Suspend
		}
	}

	// Overlay-specific routing
	switch b.view {
	case viewDetail:
		return b.updateDetail(msg)
	case viewForm:
		return b.updateForm(msg)
	case viewSearch:
		return b.updateSearch(msg)
	}

	switch msg := msg.(type) {

	case moveMsg:
		return b, b.handleMove(msg)

	case deleteMsg:
		return b, persistDelete(b.store, msg.id)

	case priorityMsg:
		return b, b.handlePriority(msg)

	case saveMsg:
		return b, b.handleSave(msg)

	case tea.MouseMsg:
		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
			targetCol, ok := b.columnAtX(msg.X)
			if !ok {
				return b, nil
			}
			if msg.Ctrl {
				// Ctrl+click: move selected card to clicked column.
				if targetCol == b.focused {
					return b, nil
				}
				cd, cardOk := b.cols[b.focused].SelectedCard()
				if !cardOk {
					return b, nil
				}
				src := b.focused
				return b, func() tea.Msg {
					return moveMsg{card: cd, source: src, target: targetCol}
				}
			}
			// Plain click: focus the clicked column.
			if targetCol != b.focused {
				b.moveFocus(int(targetCol - b.focused))
			}
			return b, nil
		}

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, keys.Quit):
			b.quitting = true
			return b, tea.Quit

		case key.Matches(msg, keys.Left):
			b.moveFocus(-1)
			return b, nil

		case key.Matches(msg, keys.Right):
			b.moveFocus(1)
			return b, nil

		case key.Matches(msg, keys.New):
			b.openNewForm()
			return b, textinputBlink()

		case key.Matches(msg, keys.Edit):
			b.openEditForm()
			return b, textinputBlink()

		case key.Matches(msg, keys.Detail):
			b.openDetail()
			return b, nil

		case key.Matches(msg, keys.Undo):
			return b, b.undoLastMove()

		case key.Matches(msg, keys.Help):
			b.help.ShowAll = !b.help.ShowAll
			return b, nil

		case key.Matches(msg, keys.Search):
			b.openSearch()
			return b, textinputBlink()

		}
	}

	// Forward remaining messages to the focused column
	cmd := b.cols[b.focused].Update(msg)
	return b, cmd
}

func (b *board) View() string {
	if b.quitting {
		return ""
	}
	if !b.loaded {
		return "Loading..."
	}
	switch b.view {
	case viewDetail:
		return b.detail.View()
	case viewForm:
		return b.form.View()
	}

	// Build visible columns based on panning
	visible := b.visibleCount()
	var views []string
	for i := 0; i < visible && b.panOffset+i < int(numColumns); i++ {
		idx := b.panOffset + i
		views = append(views, b.cols[idx].View())
	}

	columnsView := lipgloss.JoinHorizontal(lipgloss.Top, views...)

	// Position indicator
	indicator := b.positionIndicator()

	// Error display
	var errView string
	if b.err != nil {
		errView = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Render("Error: " + b.err.Error())
	}

	var footerView string
	if b.view == viewSearch {
		footerView = b.searchBarView()
	} else {
		footerView = b.help.View(keys)
	}

	full := lipgloss.JoinVertical(lipgloss.Left,
		"", // breathing room above column headings
		columnsView,
		indicator,
		errView,
		footerView,
	)

	return lipgloss.NewStyle().MaxWidth(b.termWidth).Render(full)
}

// loadFromStore returns a command that loads all issues and sets up column items.
func (b *board) loadFromStore() tea.Cmd {
	return func() tea.Msg {
		issues, err := b.store.ListIssues()
		if err != nil {
			return errMsg{err}
		}
		return refreshMsg{issues: issues}
	}
}

// applyRefresh partitions issues by status and updates column lists.
// When search is active, the full item set is stashed in allItems and only
// matching items are shown so the live filter stays consistent across polls.
func (b *board) applyRefresh(issues []*beadslite.Issue) {
	buckets := partitionByStatus(issues)
	for i := columnIndex(0); i < numColumns; i++ {
		items := buckets[i]
		if items == nil {
			items = []list.Item{}
		}
		if b.view == viewSearch {
			b.allItems[i] = items
			b.cols[i].SetItems(filterItems(items, b.searchInput.Value()))
		} else {
			b.cols[i].SetItems(items)
		}
	}
}

// handleMove inserts a card into the target column, shifts focus to follow it,
// and persists the status change. Saves state for single-level undo.
func (b *board) handleMove(msg moveMsg) tea.Cmd {
	result := computeMove(msg.card, msg.source, msg.target)
	if result == nil {
		return nil
	}

	// Save for undo before mutating the card's status.
	b.lastMove = &moveMsg{
		card:   msg.card,
		source: msg.source,
		target: msg.target,
	}

	msg.card.issue.Status = result.newStatus

	// Remove from source column (atomic: both remove and add happen here)
	srcItems := b.cols[result.source].list.Items()
	for i, item := range srcItems {
		if c, ok := item.(card); ok && c.issue.ID == result.cardID {
			b.cols[result.source].list.RemoveItem(i)
			break
		}
	}

	// Add to target column
	items := b.cols[result.target].list.Items()
	items = append(items, msg.card)
	b.cols[result.target].SetItems(items)

	// Follow the card: shift focus to the target column and select it
	b.cols[b.focused].Blur()
	b.focused = result.target
	b.cols[b.focused].Focus()
	b.cols[result.target].list.Select(len(items) - 1)
	b.updatePan()
	b.resizeColumns()

	return persistMove(b.store, result.cardID, result.target)
}

// handlePriority adjusts a card's priority, re-sorts the column, and persists.
func (b *board) handlePriority(msg priorityMsg) tea.Cmd {
	newPriority := computePriority(msg.card.issue.Priority, msg.delta)
	if newPriority < 0 {
		return nil // already at boundary
	}

	msg.card.issue.Priority = newPriority

	// Re-sort the focused column to reflect the new priority order
	col := &b.cols[b.focused]
	items := col.list.Items()
	sort.Slice(items, func(a, bIdx int) bool {
		ca := items[a].(card)
		cb := items[bIdx].(card)
		return ca.issue.Priority < cb.issue.Priority
	})
	col.SetItems(items)

	// Re-select the card that was adjusted
	for i, item := range col.list.Items() {
		if c, ok := item.(card); ok && c.issue.ID == msg.card.issue.ID {
			col.list.Select(i)
			break
		}
	}

	return persistUpdate(b.store, msg.card.issue)
}

// undoLastMove reverses the most recent card move.
func (b *board) undoLastMove() tea.Cmd {
	result := computeUndo(b.lastMove)
	if result == nil {
		return nil
	}

	undo := b.lastMove
	b.lastMove = nil

	// Remove the card from where it landed (result.source = where it currently is)
	items := b.cols[result.source].list.Items()
	for i, item := range items {
		if c, ok := item.(card); ok && c.issue.ID == result.cardID {
			b.cols[result.source].list.RemoveItem(i)
			break
		}
	}

	// Put it back in the original column (result.target = where it goes back to)
	undo.card.issue.Status = result.newStatus
	srcItems := b.cols[result.target].list.Items()
	srcItems = append(srcItems, undo.card)
	b.cols[result.target].SetItems(srcItems)

	// Follow the card back
	b.cols[b.focused].Blur()
	b.focused = result.target
	b.cols[b.focused].Focus()
	b.cols[result.target].list.Select(len(srcItems) - 1)
	b.updatePan()
	b.resizeColumns()

	return persistMove(b.store, result.cardID, result.target)
}

// handleSave processes a form submission (create or edit).
func (b *board) handleSave(msg saveMsg) tea.Cmd {
	b.view = viewBoard
	b.form = nil

	if msg.issue == nil {
		return nil
	}

	if msg.issue.ID == "" {
		// This shouldn't happen — NewIssue always sets an ID
		return nil
	}

	// Check if this is an edit (issue already exists in a column)
	for i := columnIndex(0); i < numColumns; i++ {
		for j, item := range b.cols[i].list.Items() {
			if c, ok := item.(card); ok && c.issue.ID == msg.issue.ID {
				// Update in place
				b.cols[i].list.SetItem(j, card{issue: msg.issue})
				return persistUpdate(b.store, msg.issue)
			}
		}
	}

	// New card: add to the appropriate column
	col := statusToColumn[msg.issue.Status]
	items := b.cols[col].list.Items()
	items = append(items, card{issue: msg.issue})
	b.cols[col].SetItems(items)
	return persistCreate(b.store, msg.issue)
}

// moveFocus shifts focus by delta columns (-1 or +1).
func (b *board) moveFocus(delta int) {
	next := int(b.focused) + delta
	if next < 0 || next >= int(numColumns) {
		return
	}

	b.cols[b.focused].Blur()
	b.focused = columnIndex(next)
	b.cols[b.focused].Focus()
	b.updatePan()
	b.resizeColumns()
}

// openNewForm switches to form mode for creating a new card.
func (b *board) openNewForm() {
	f := newForm(b.focused)
	f.width = b.termWidth
	f.height = b.termHeight
	b.form = &f
	b.view = viewForm
}

// openEditForm switches to form mode for editing the selected card.
func (b *board) openEditForm() {
	cd, ok := b.cols[b.focused].SelectedCard()
	if !ok {
		return
	}
	f := editForm(cd.issue, b.focused)
	f.width = b.termWidth
	f.height = b.termHeight
	b.form = &f
	b.view = viewForm
}

// openDetail switches to detail mode showing the selected card.
func (b *board) openDetail() {
	cd, ok := b.cols[b.focused].SelectedCard()
	if !ok {
		return
	}
	d := newDetail(cd.issue, b.focused)
	d.width = b.termWidth
	d.height = b.termHeight
	b.detail = &d
	b.view = viewDetail
}

// updateDetail routes messages to the detail overlay.
// esc closes, e switches to edit form.
func (b *board) updateDetail(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if key.Matches(msg, keys.Back) {
			b.view = viewBoard
			b.detail = nil
			return b, nil
		}
		if key.Matches(msg, keys.Edit) {
			// Transition from detail to edit
			issue := b.detail.issue
			colIdx := b.detail.columnIndex
			b.detail = nil
			f := editForm(issue, colIdx)
			f.width = b.termWidth
			f.height = b.termHeight
			b.form = &f
			b.view = viewForm
			return b, textinputBlink()
		}
	}

	if b.detail != nil {
		d, cmd := b.detail.Update(msg)
		b.detail = &d
		return b, cmd
	}
	return b, nil
}

// updateForm routes messages to the form overlay.
func (b *board) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if key.Matches(msg, keys.Back) {
			b.view = viewBoard
			b.form = nil
			return b, nil
		}
	case saveMsg:
		return b, b.handleSave(msg)
	}

	if b.form != nil {
		f, cmd := b.form.Update(msg)
		b.form = &f
		return b, cmd
	}
	return b, nil
}

// Layout panning

const minColumnWidth = 24

func (b *board) visibleCount() int {
	if b.termWidth == 0 {
		return int(numColumns)
	}
	count := b.termWidth / minColumnWidth
	if count < 1 {
		count = 1
	}
	if count > int(numColumns) {
		count = int(numColumns)
	}
	return count
}

// columnAtX maps a mouse X coordinate to the column at that position.
// Returns false if the coordinate falls outside any visible column.
func (b *board) columnAtX(x int) (columnIndex, bool) {
	visible := b.visibleCount()
	if visible == 0 {
		return 0, false
	}
	if x < 0 {
		return 0, false
	}
	colWidth := b.termWidth / visible
	if colWidth == 0 {
		return 0, false
	}
	col := x / colWidth
	if col < 0 || col >= visible {
		return 0, false
	}
	idx := columnIndex(b.panOffset + col)
	if idx >= numColumns {
		return 0, false
	}
	return idx, true
}

// updatePan adjusts panOffset so the focused column is visible.
func (b *board) updatePan() {
	visible := b.visibleCount()
	focusIdx := int(b.focused)

	if focusIdx < b.panOffset {
		b.panOffset = focusIdx
	}
	if focusIdx >= b.panOffset+visible {
		b.panOffset = focusIdx - visible + 1
	}
	// Clamp
	maxOffset := int(numColumns) - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	if b.panOffset > maxOffset {
		b.panOffset = maxOffset
	}
}

// resizeColumns distributes terminal width evenly among visible columns.
func (b *board) resizeColumns() {
	visible := b.visibleCount()
	if visible == 0 {
		return
	}

	// Reserve space for top padding, help bar, and position indicator
	colHeight := b.termHeight - 5
	if colHeight < 5 {
		colHeight = 5
	}

	// Each column's border (visible or hidden) adds 2 chars (left + right).
	// Subtract that so the total rendered width fits within termWidth.
	const borderWidth = 2
	colWidth := (b.termWidth / visible) - borderWidth

	for i := 0; i < visible && b.panOffset+i < int(numColumns); i++ {
		idx := b.panOffset + i
		b.cols[idx].SetSize(colWidth, colHeight)
	}
}

// positionIndicator shows which columns are visible: [< Backlog | *To Do* | Doing >]
func (b *board) positionIndicator() string {
	visible := b.visibleCount()
	if visible >= int(numColumns) {
		return "" // all visible, no indicator needed
	}

	var parts []string
	if b.panOffset > 0 {
		parts = append(parts, "<")
	} else {
		parts = append(parts, " ")
	}

	for i := 0; i < visible && b.panOffset+i < int(numColumns); i++ {
		idx := b.panOffset + i
		name := columnTitles[idx]
		if columnIndex(idx) == b.focused {
			name = "*" + name + "*"
		}
		parts = append(parts, name)
	}

	if b.panOffset+visible < int(numColumns) {
		parts = append(parts, ">")
	} else {
		parts = append(parts, " ")
	}

	indicator := ""
	for i, p := range parts {
		if i > 0 && i < len(parts)-1 {
			indicator += " | "
		}
		indicator += p
	}

	return lipgloss.NewStyle().
		Faint(true).
		Width(b.termWidth).
		Align(lipgloss.Center).
		Render("[" + indicator + "]")
}

// openSearch enters search mode: snapshot all column items and activate the input.
func (b *board) openSearch() {
	for i := columnIndex(0); i < numColumns; i++ {
		b.allItems[i] = b.cols[i].list.Items()
	}
	b.searchInput.Reset()
	b.searchInput.Focus()
	b.view = viewSearch
}

// cancelSearch restores all columns to their pre-search item sets and exits search mode.
func (b *board) cancelSearch() {
	for i := columnIndex(0); i < numColumns; i++ {
		b.cols[i].SetItems(b.allItems[i])
	}
	b.searchInput.Blur()
	b.view = viewBoard
}

// dismissSearch exits search mode without restoring the full list. The filter is transient —
// the next 2-second refresh tick replaces the filtered view with full data from SQLite.
func (b *board) dismissSearch() {
	b.searchInput.Blur()
	b.view = viewBoard
}

// updateSearch handles input while in search mode.
func (b *board) updateSearch(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, keys.Back):
			b.cancelSearch()
			return b, nil
		case msg.Type == tea.KeyEnter:
			b.dismissSearch()
			return b, nil
		}
	}

	// Forward to text input and re-filter on every change.
	prevQuery := b.searchInput.Value()
	var cmd tea.Cmd
	b.searchInput, cmd = b.searchInput.Update(msg)

	if b.searchInput.Value() != prevQuery {
		b.applySearchFilter(b.searchInput.Value())
	}

	return b, cmd
}

// applySearchFilter updates each column to show only items matching query.
func (b *board) applySearchFilter(query string) {
	for i := columnIndex(0); i < numColumns; i++ {
		b.cols[i].SetItems(filterItems(b.allItems[i], query))
	}
}

// filterItems returns only items whose title or body text contain query (case-insensitive).
// An empty query returns all items unchanged.
func filterItems(items []list.Item, query string) []list.Item {
	if query == "" {
		return items
	}
	q := strings.ToLower(query)
	var out []list.Item
	for _, item := range items {
		c, ok := item.(card)
		if !ok {
			continue
		}
		if strings.Contains(strings.ToLower(c.FilterValue()), q) ||
			strings.Contains(strings.ToLower(c.issue.Description), q) {
			out = append(out, item)
		}
	}
	if out == nil {
		out = []list.Item{}
	}
	return out
}

// searchBarView renders the search input in place of the help bar.
func (b *board) searchBarView() string {
	label := lipgloss.NewStyle().
		Foreground(lipgloss.Color("170")).
		Bold(true).
		Render("/ ")

	hint := lipgloss.NewStyle().
		Faint(true).
		Render("  enter: accept  esc: cancel")

	return label + b.searchInput.View() + hint
}

// textinputBlink returns a command to start the text input cursor blinking.
func textinputBlink() tea.Cmd {
	return textinput.Blink
}
