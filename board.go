package main

import (
	"fmt"
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
	viewBoard      boardView = iota // default: show columns
	viewForm                        // create/edit form overlay
	viewDetail                      // card detail overlay
	viewSearch                      // cross-column search mode
	viewResolution                  // resolution picker before closing a card
	viewDepLink                     // dep-link picker: link focused card to a blocker
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

	// Overlay state: view controls routing; form/detail/resolution/depLinker hold overlay data.
	view         boardView
	form         *form
	detail       *detail
	resolution   *resolutionPicker
	depLinker    *depLinker

	// Undo stack for moves, priority changes, edits, and deletes.
	// Capped at maxUndoStack entries; oldest entry is dropped when full.
	undo undoStack

	// Search state: input holds the query; allItems caches the full per-column
	// lists so we can restore them when search is cancelled.
	searchInput textinput.Model
	allItems    [numColumns][]list.Item

	// Filter state: activeFilter narrows visible cards by priority, type, or assignee.
	// allIssues caches every issue so filter steps can be rebuilt from the full set
	// (not just what's currently visible after a previous filter was applied).
	// allBlockedIDs mirrors the blockedIDs from the latest refresh so the lock icon
	// indicator is preserved when cycling filters between poll ticks.
	filter        activeFilter
	allIssues     []*beadslite.Issue
	allBlockedIDs map[string]bool

	// Layout panning
	termWidth      int
	termHeight     int
	panOffset      int  // index of first visible column (horizontal mode)
	verticalLayout bool // true = columns stacked top-to-bottom, false = side-by-side (default)

	// wip holds per-column WIP limits loaded from .ralph-ban/config.json.
	// Zero limit for a column means unlimited.
	wip boardConfig
}

func newBoard(store *beadslite.Store) *board {
	// Load WIP config before constructing columns so limits are available
	// during the first render. Missing or malformed config is silently ignored.
	wip := loadConfig(ralphBanDir)

	var cols [numColumns]column
	for i := columnIndex(0); i < numColumns; i++ {
		cols[i] = newColumn(i)
		cols[i].wipLimit = wip.wipLimit(i)
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
		store:        store,
		cols:         cols,
		help:         h,
		searchInput:  si,
		wip: wip,
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
		if b.resolution != nil {
			b.resolution.width = msg.Width
			b.resolution.height = msg.Height
		}
		b.updatePan()
		b.resizeColumns()
		return b, nil

	case refreshMsg:
		b.err = nil
		b.applyRefresh(msg)
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
	case viewResolution:
		return b.updateResolution(msg)
	case viewDepLink:
		return b.updateDepLink(msg)
	}

	switch msg := msg.(type) {

	case moveMsg:
		return b, b.handleMove(msg)

	case deleteMsg:
		return b, b.handleDelete(msg)

	case priorityMsg:
		return b, b.handlePriority(msg)

	case saveMsg:
		return b, b.handleSave(msg)

	case closeMsg:
		return b, b.handleClose(msg)

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
			return b, b.undoLast()

		case key.Matches(msg, keys.Help):
			b.help.ShowAll = !b.help.ShowAll
			return b, nil

		case key.Matches(msg, keys.Search):
			b.openSearch()
			return b, textinputBlink()

		case key.Matches(msg, keys.BlockedBy):
			b.openDepLinker(depModeBlockedBy)
			return b, nil

		case key.Matches(msg, keys.Blocks):
			b.openDepLinker(depModeBlocks)
			return b, nil

		case key.Matches(msg, keys.FilterNext):
			b.cycleFilter(+1)
			return b, nil

		case key.Matches(msg, keys.FilterPrev):
			b.cycleFilter(-1)
			return b, nil

		case key.Matches(msg, keys.LayoutToggle):
			b.verticalLayout = !b.verticalLayout
			b.updatePan()
			b.resizeColumns()
			return b, nil

		case key.Matches(msg, keys.Back):
			if b.filter.field != filterNone {
				b.clearFilter()
				return b, nil
			}

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
	case viewResolution:
		if b.resolution != nil {
			return b.resolution.View()
		}
	case viewDepLink:
		if b.depLinker != nil {
			return b.depLinker.View()
		}
	}

	var columnsView string
	var indicator string

	if b.verticalLayout {
		columnsView = b.viewVertical()
		indicator = b.positionIndicatorVertical()
	} else {
		// Horizontal: columns side by side, panning horizontally.
		visible := b.visibleCount()
		var views []string
		for i := 0; i < visible && b.panOffset+i < int(numColumns); i++ {
			idx := b.panOffset + i
			views = append(views, b.cols[idx].View())
		}
		columnsView = lipgloss.JoinHorizontal(lipgloss.Top, views...)
		indicator = b.positionIndicator()
	}

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
	} else if b.filter.field != filterNone {
		// When a filter is active, replace the help bar with the cycle indicator
		// so the user can see where they are in the filter cycle.
		footerView = filterCycleView(b.filter, b.allIssues, 7)
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

// viewVertical renders columns stacked top-to-bottom as full-width horizontal bands.
// Only a window of columns that fit the terminal height are shown (similar to panOffset
// but vertical). The focused column is always visible.
func (b *board) viewVertical() string {
	visible := b.visibleCountVertical()
	var bands []string
	for i := 0; i < visible && b.panOffset+i < int(numColumns); i++ {
		idx := b.panOffset + i
		bands = append(bands, b.cols[idx].ViewVertical(b.termWidth))
	}
	return lipgloss.JoinVertical(lipgloss.Left, bands...)
}

// findCardIndex returns the index of the card with the given ID in items, or -1.
func findCardIndex(items []list.Item, id string) int {
	for i, item := range items {
		if c, ok := item.(card); ok && c.issue.ID == id {
			return i
		}
	}
	return -1
}

// findCardInBoard searches all columns for a card by ID.
// Returns the column index, item index, and whether it was found.
func findCardInBoard(cols [numColumns]column, id string) (int, int, bool) {
	for ci := range cols {
		items := cols[ci].list.Items()
		if idx := findCardIndex(items, id); idx >= 0 {
			return ci, idx, true
		}
	}
	return 0, 0, false
}

// sortByPriority sorts items by ascending priority (P0 first).
func sortByPriority(items []list.Item) {
	sort.Slice(items, func(a, b int) bool {
		return items[a].(card).issue.Priority < items[b].(card).issue.Priority
	})
}

// loadFromStore returns a command that loads all issues and sets up column items.
func (b *board) loadFromStore() tea.Cmd {
	return func() tea.Msg {
		return fetchRefresh(b.store)
	}
}

// applyRefresh partitions issues by status and updates column lists.
// When search is active, the full item set is stashed in allItems and only
// matching items are shown so the live filter stays consistent across polls.
// When a filter is active, each column's items are narrowed to only matching cards.
// The undo stack is cleared because external writes may have made recorded
// operations stale — applying them after a refresh could corrupt state.
func (b *board) applyRefresh(msg refreshMsg) {
	// Undo entries recorded before this refresh may no longer reflect reality
	// (another session or CLI could have modified the same cards). Clear the
	// stack so the user can't undo against a snapshot that no longer exists.
	b.undo.clear()

	// Cache the full issue list and blocked IDs so filter steps can be rebuilt
	// from all issues without losing the lock icon indicator between poll ticks.
	b.allIssues = msg.issues
	b.allBlockedIDs = msg.blockedIDs

	buckets := partitionByStatus(msg.issues, msg.blockedIDs)
	for i := columnIndex(0); i < numColumns; i++ {
		items := buckets[i]
		if items == nil {
			items = []list.Item{}
		}

		// Apply column filter before storing or displaying.
		filtered := applyFilterToItems(items, b.filter)

		if b.view == viewSearch {
			b.allItems[i] = filtered
			b.cols[i].SetItems(filterItems(filtered, b.searchInput.Value()))
		} else {
			b.cols[i].SetItems(filtered)
		}
	}
}

// handleMove inserts a card into the target column, shifts focus to follow it,
// and persists the status change. Saves state for single-level undo.
// If the target column has a WIP limit that would be exceeded, the move is
// blocked and an error is shown in the status bar instead.
// Moving a card into Done opens the resolution picker instead of persisting
// immediately, so that ClosedAt, resolution, and AssignedTo clearing are set.
func (b *board) handleMove(msg moveMsg) tea.Cmd {
	result := computeMove(msg.card, msg.source, msg.target)
	if result == nil {
		return nil
	}

	// Intercept moves into Done: open resolution picker instead.
	// The picker will emit a closeMsg when the user confirms.
	if result.target == colDone {
		picker := newResolutionPicker(msg.card, msg.source)
		picker.width = b.termWidth
		picker.height = b.termHeight
		b.resolution = &picker
		b.view = viewResolution
		return nil
	}

	// Enforce WIP limit before mutating any state.
	if limit := b.wip.wipLimit(result.target); limit > 0 {
		current := len(b.cols[result.target].list.Items())
		if current >= limit {
			b.err = fmt.Errorf(
				"WIP limit reached: %s is at capacity (%d/%d)",
				columnTitles[result.target], current, limit,
			)
			return nil
		}
	}

	// Clear any prior WIP error now that the move is proceeding.
	b.err = nil

	return b.applyMove(msg.card, result)
}

// applyColumnMove performs the column mutation shared by regular moves and closes:
// push undo, update status, remove from source, add to target, shift focus, update layout.
// The caller is responsible for persisting the change (persistMove vs persistClose).
func (b *board) applyColumnMove(cd card, result *moveResult) {
	b.undo.push(undoEntry{
		kind: undoMove,
		move: &moveMsg{
			card:   cd,
			source: result.source,
			target: result.target,
		},
	})

	cd.issue.Status = result.newStatus

	if idx := findCardIndex(b.cols[result.source].list.Items(), result.cardID); idx >= 0 {
		b.cols[result.source].list.RemoveItem(idx)
	}

	items := b.cols[result.target].list.Items()
	items = append(items, cd)
	b.cols[result.target].SetItems(items)

	b.cols[b.focused].Blur()
	b.focused = result.target
	b.cols[b.focused].Focus()
	b.cols[result.target].list.Select(len(items) - 1)
	b.updatePan()
	b.resizeColumns()
}

// applyMove mutates column state and persists the status change.
func (b *board) applyMove(cd card, result *moveResult) tea.Cmd {
	b.applyColumnMove(cd, result)
	return persistMove(b.store, result.cardID, result.target)
}

// handlePriority adjusts a card's priority, re-sorts the column, and persists.
func (b *board) handlePriority(msg priorityMsg) tea.Cmd {
	newPriority := computePriority(msg.card.issue.Priority, msg.delta)
	if newPriority < 0 {
		return nil // already at boundary
	}

	// Record old priority before mutating so the user can undo the change.
	b.undo.push(undoEntry{
		kind:           undoPriority,
		priorityCardID: msg.card.issue.ID,
		priorityCol:    b.focused,
		oldPriority:    msg.card.issue.Priority,
	})

	msg.card.issue.Priority = newPriority

	// Re-sort the focused column to reflect the new priority order
	col := &b.cols[b.focused]
	items := col.list.Items()
	sortByPriority(items)
	col.SetItems(items)

	// Re-select the card that was adjusted
	if ri := findCardIndex(col.list.Items(), msg.card.issue.ID); ri >= 0 {
		col.list.Select(ri)
	}

	return persistUpdate(b.store, msg.card.issue)
}

// undoLast pops the most recent entry from the undo stack and reverses it.
// Each press of 'u' walks one step further back through the operation history.
func (b *board) undoLast() tea.Cmd {
	entry, ok := b.undo.pop()
	if !ok {
		return nil
	}

	switch entry.kind {
	case undoMove:
		return b.applyUndoMove(entry.move)
	case undoPriority:
		return b.applyUndoPriority(entry)
	case undoEdit:
		return b.applyUndoEdit(entry.issue)
	case undoDelete:
		return b.applyUndoDelete(entry.issue)
	}
	return nil
}

// applyUndoMove reverses a column move by moving the card back to its source column.
func (b *board) applyUndoMove(lastMove *moveMsg) tea.Cmd {
	result := computeUndoMove(lastMove)
	if result == nil {
		return nil
	}

	// Remove the card from where it landed (result.source = current location)
	if idx := findCardIndex(b.cols[result.source].list.Items(), result.cardID); idx >= 0 {
		b.cols[result.source].list.RemoveItem(idx)
	}

	// Put it back in the original column (result.target = original location)
	lastMove.card.issue.Status = result.newStatus
	srcItems := b.cols[result.target].list.Items()
	srcItems = append(srcItems, lastMove.card)
	b.cols[result.target].SetItems(srcItems)

	// Follow the card back to its original column
	b.cols[b.focused].Blur()
	b.focused = result.target
	b.cols[b.focused].Focus()
	b.cols[result.target].list.Select(len(srcItems) - 1)
	b.updatePan()
	b.resizeColumns()

	return persistMove(b.store, result.cardID, result.target)
}

// applyUndoPriority restores a card's priority to its value before the last change.
func (b *board) applyUndoPriority(entry undoEntry) tea.Cmd {
	col := &b.cols[entry.priorityCol]
	items := col.list.Items()

	// Find the card and restore its old priority.
	idx := findCardIndex(items, entry.priorityCardID)
	if idx < 0 {
		return nil // card no longer in this column (external change)
	}
	items[idx].(card).issue.Priority = entry.oldPriority
	targetIssue := items[idx].(card).issue

	// Re-sort the column with the restored priority.
	sortByPriority(items)
	col.SetItems(items)

	// Re-select the card that was restored.
	if ri := findCardIndex(col.list.Items(), entry.priorityCardID); ri >= 0 {
		col.list.Select(ri)
	}

	return persistUpdate(b.store, targetIssue)
}

// applyUndoEdit restores an issue to its state before the last edit.
func (b *board) applyUndoEdit(oldIssue *beadslite.Issue) tea.Cmd {
	col := statusToColumn[oldIssue.Status]
	if idx := findCardIndex(b.cols[col].list.Items(), oldIssue.ID); idx >= 0 {
		b.cols[col].list.SetItem(idx, card{issue: oldIssue})
		return persistUpdate(b.store, oldIssue)
	}
	return nil // card no longer present (external change)
}

// applyUndoDelete re-creates a card that was deleted by the user.
func (b *board) applyUndoDelete(issue *beadslite.Issue) tea.Cmd {
	col := statusToColumn[issue.Status]
	items := b.cols[col].list.Items()
	items = append(items, card{issue: issue})
	b.cols[col].SetItems(items)
	return persistCreate(b.store, issue)
}

// handleDelete snapshots the card before deletion so the operation can be undone.
func (b *board) handleDelete(msg deleteMsg) tea.Cmd {
	// Search all columns for the card so we can snapshot it.
	if ci, ii, ok := findCardInBoard(b.cols, msg.id); ok {
		c := b.cols[ci].list.Items()[ii].(card)
		// Deep-copy the issue so the undo entry is independent of
		// any subsequent pointer mutations.
		snapshot := *c.issue
		b.undo.push(undoEntry{
			kind:  undoDelete,
			issue: &snapshot,
		})
	}
	return persistDelete(b.store, msg.id)
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
	if ci, ii, ok := findCardInBoard(b.cols, msg.issue.ID); ok {
		c := b.cols[ci].list.Items()[ii].(card)
		// Snapshot the old state before overwriting so the edit can be undone.
		oldIssue := *c.issue
		b.undo.push(undoEntry{
			kind:  undoEdit,
			issue: &oldIssue,
		})
		// Update in place
		b.cols[ci].list.SetItem(ii, card{issue: msg.issue})
		return persistUpdate(b.store, msg.issue)
	}

	// New card: add to the appropriate column
	col := statusToColumn[msg.issue.Status]
	items := b.cols[col].list.Items()
	items = append(items, card{issue: msg.issue})
	b.cols[col].SetItems(items)
	return persistCreate(b.store, msg.issue)
}

// handleClose finalizes a card move into Done using CloseIssue so that
// resolution, ClosedAt, and AssignedTo are all set correctly.
func (b *board) handleClose(msg closeMsg) tea.Cmd {
	result := computeMove(msg.card, msg.source, colDone)
	if result == nil {
		return nil
	}

	// Enforce WIP limit on Done column.
	if limit := b.wip.wipLimit(colDone); limit > 0 {
		current := len(b.cols[colDone].list.Items())
		if current >= limit {
			b.err = fmt.Errorf(
				"WIP limit reached: %s is at capacity (%d/%d)",
				columnTitles[colDone], current, limit,
			)
			return nil
		}
	}

	b.err = nil

	// Reuse applyColumnMove for undo push, status update, column mutation, and layout.
	b.applyColumnMove(msg.card, result)

	return persistClose(b.store, result.cardID, msg.resolution)
}

// openDepLinker opens the dep-link picker for the focused card.
// Does nothing if no card is selected.
func (b *board) openDepLinker(mode depLinkMode) {
	cd, ok := b.cols[b.focused].SelectedCard()
	if !ok {
		return
	}
	dl := newDepLinker(b.allIssues, cd.issue.ID, mode)
	dl.width = b.termWidth
	dl.height = b.termHeight
	b.depLinker = &dl
	b.view = viewDepLink
}

// updateDepLink routes messages to the dep-link picker overlay.
// Esc cancels. Enter (emitted as depLinkMsg) creates the dependency link.
func (b *board) updateDepLink(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if key.Matches(msg, keys.Back) {
			b.view = viewBoard
			b.depLinker = nil
			return b, nil
		}
	case depLinkMsg:
		b.view = viewBoard
		b.depLinker = nil
		return b, b.handleDepLink(msg)
	}

	if b.depLinker != nil {
		dl, cmd := b.depLinker.Update(msg)
		b.depLinker = &dl
		return b, cmd
	}
	return b, nil
}

// handleDepLink persists the dependency link and triggers a refresh so the
// board immediately reflects the new blocker state (locked indicators etc.).
func (b *board) handleDepLink(msg depLinkMsg) tea.Cmd {
	var issueID, dependsOnID string
	switch msg.mode {
	case depModeBlockedBy:
		// focused card is blocked by the picked card
		issueID = msg.focusedID
		dependsOnID = msg.pickedID
	case depModeBlocks:
		// focused card blocks the picked card
		issueID = msg.pickedID
		dependsOnID = msg.focusedID
	}

	store := b.store
	return tea.Sequence(
		persistAddDep(store, issueID, dependsOnID),
		b.loadFromStore(),
	)
}

// updateResolution routes messages to the resolution picker overlay.
// Esc cancels and restores the board view. Enter (emitted as closeMsg) finalizes.
func (b *board) updateResolution(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if key.Matches(msg, keys.Back) {
			b.view = viewBoard
			b.resolution = nil
			return b, nil
		}
	case closeMsg:
		b.view = viewBoard
		b.resolution = nil
		return b, b.handleClose(msg)
	}

	if b.resolution != nil {
		r, cmd := b.resolution.Update(msg)
		b.resolution = &r
		return b, cmd
	}
	return b, nil
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
// Dependencies are resolved here so the detail overlay stays a pure renderer.
func (b *board) openDetail() {
	cd, ok := b.cols[b.focused].SelectedCard()
	if !ok {
		return
	}

	blockedBy := b.resolveBlockedBy(cd.issue.ID)
	blocks := b.resolveBlocks(cd.issue.ID)

	d := newDetail(cd.issue, b.focused, blockedBy, blocks)
	d.width = b.termWidth
	d.height = b.termHeight
	b.detail = &d
	b.view = viewDetail
}

// resolveBlockedBy returns the issues that block the given card.
// GetDependencies returns rows where issue_id = id, meaning id depends on those issues.
// Only DepBlocks entries are shown; DepParent (epic links) are filtered out.
func (b *board) resolveBlockedBy(id string) []depEntry {
	deps, err := b.store.GetDependencies(id)
	if err != nil {
		return nil
	}
	var entries []depEntry
	for _, dep := range deps {
		if dep.Type != beadslite.DepBlocks {
			continue
		}
		blocker, err := b.store.GetIssue(dep.DependsOnID)
		if err != nil {
			// Dangling ref — skip rather than surface a store error in the UI.
			continue
		}
		entries = append(entries, depEntry{id: blocker.ID, title: blocker.Title})
	}
	return entries
}

// resolveBlocks returns the issues that are waiting on the given card.
// GetAllDependencies gives us all deps keyed by dependent issue_id; we scan
// for rows where DependsOnID == id to find the reverse relationship.
func (b *board) resolveBlocks(id string) []depEntry {
	allDeps, err := b.store.GetAllDependencies()
	if err != nil {
		return nil
	}
	var entries []depEntry
	for dependentID, deps := range allDeps {
		for _, dep := range deps {
			if dep.Type != beadslite.DepBlocks || dep.DependsOnID != id {
				continue
			}
			dependent, err := b.store.GetIssue(dependentID)
			if err != nil {
				continue
			}
			entries = append(entries, depEntry{id: dependent.ID, title: dependent.Title})
		}
	}
	return entries
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

// minColumnBandHeight is the minimum height (in terminal rows) for one vertical-mode band.
// Each band has a header line plus at least a few card lines.
const minColumnBandHeight = 4

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

// visibleCountVertical returns how many column bands fit vertically.
// Reserves space for the breathing-room line, indicator, error, and footer.
func (b *board) visibleCountVertical() int {
	if b.termHeight == 0 {
		return int(numColumns)
	}
	// Reserve: 1 (top padding) + 1 (indicator) + 1 (footer) = 3 lines overhead
	available := b.termHeight - 3
	if available < minColumnBandHeight {
		available = minColumnBandHeight
	}
	count := available / minColumnBandHeight
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
// Works for both horizontal and vertical layout — panOffset is always an index
// into the column array; the meaning of "window" changes with orientation.
func (b *board) updatePan() {
	var visible int
	if b.verticalLayout {
		visible = b.visibleCountVertical()
	} else {
		visible = b.visibleCount()
	}
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

// resizeColumns distributes terminal dimensions among visible columns.
// Horizontal: evenly divides terminal width; all columns share the same height.
// Vertical: each band gets the full terminal width; height is divided among visible bands.
func (b *board) resizeColumns() {
	if b.verticalLayout {
		b.resizeColumnsVertical()
		return
	}

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

// resizeColumnsVertical sets dimensions for vertical-mode bands.
// Each band gets the full terminal width and an equal share of available height.
func (b *board) resizeColumnsVertical() {
	visible := b.visibleCountVertical()
	if visible == 0 {
		return
	}

	// Reserve: 1 (top padding) + 1 (indicator) + 1 (footer) = 3 rows overhead
	available := b.termHeight - 3
	if available < minColumnBandHeight {
		available = minColumnBandHeight
	}

	bandHeight := available / visible
	if bandHeight < minColumnBandHeight {
		bandHeight = minColumnBandHeight
	}

	// Full terminal width minus border (2) for each band.
	const borderWidth = 2
	bandWidth := b.termWidth - borderWidth

	for i := 0; i < visible && b.panOffset+i < int(numColumns); i++ {
		idx := b.panOffset + i
		b.cols[idx].SetSize(bandWidth, bandHeight)
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

// positionIndicatorVertical shows which column bands are visible in vertical mode.
// Format: [^ Backlog | *To Do* | Doing v]
func (b *board) positionIndicatorVertical() string {
	visible := b.visibleCountVertical()
	if visible >= int(numColumns) {
		return "" // all bands visible, no indicator needed
	}

	var parts []string
	if b.panOffset > 0 {
		parts = append(parts, "^")
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
		parts = append(parts, "v")
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

// cycleFilter advances (direction=+1) or retreats (direction=-1) through the filter cycle,
// then re-renders all columns with the new filter applied immediately.
func (b *board) cycleFilter(direction int) {
	if direction >= 0 {
		b.filter = nextFilter(b.filter, b.allIssues)
	} else {
		b.filter = prevFilter(b.filter, b.allIssues)
	}
	b.applyActiveFilter()
}

// clearFilter resets the filter to none and restores all column items.
func (b *board) clearFilter() {
	b.filter = activeFilter{field: filterNone}
	b.applyActiveFilter()
}

// applyActiveFilter re-applies the current filter to every column using the
// cached allIssues list so the visible set is always consistent with the filter state.
// allBlockedIDs is passed so the "[locked]" indicator is preserved between poll ticks.
func (b *board) applyActiveFilter() {
	buckets := partitionByStatus(b.allIssues, b.allBlockedIDs)
	for i := columnIndex(0); i < numColumns; i++ {
		items := buckets[i]
		if items == nil {
			items = []list.Item{}
		}
		b.cols[i].SetItems(applyFilterToItems(items, b.filter))
	}
}

