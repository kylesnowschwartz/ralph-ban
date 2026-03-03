package main

import (
	"fmt"
	"image/color"
	"io"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

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
	// wipLimit is the maximum number of cards allowed in this column.
	// 0 means unlimited (no config entry for this column).
	wipLimit int
	// collapsed is true when this column has 0 cards and should render as a
	// narrow strip. The board sets this during resizeColumns().
	collapsed bool
	// sortReversed is true when the Done column is sorted in reverse order
	// (newest first). Only meaningful for colDone; ignored on other columns.
	sortReversed bool
}

func newColumn(idx columnIndex) column {
	// Start with blurred delegate so unfocused columns never show
	// selection highlights. The board calls Focus() on column 0.
	// Use truncating delegates from the start so titles are never rendered
	// without ellipsis, even before the first Focus/Blur call.
	var delegate list.ItemDelegate
	if idx == colDone {
		delegate = newBlurredTruncatingDelegate()
	} else {
		delegate = newBlurredAgeDelegate()
	}
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
	if c.index == colDone {
		c.list.SetDelegate(newFocusedTruncatingDelegate())
	} else {
		c.list.SetDelegate(newFocusedAgeDelegate())
	}
}

func (c *column) Blur() {
	c.focus = false
	c.confirmDelete = false
	if c.index == colDone {
		c.list.SetDelegate(newBlurredTruncatingDelegate())
	} else {
		c.list.SetDelegate(newBlurredAgeDelegate())
	}
}

func (c *column) Focused() bool { return c.focus }

// SetSize updates the column's dimensions and passes them to the inner list.
func (c *column) SetSize(w, h int) {
	c.width = w
	c.height = h
	// lipgloss v2: Width(w) is the total block width including border and padding.
	// Horizontal: border(1+1) + padding(1+1) = 4. Vertical: border(1+1) = 2.
	c.list.SetSize(w-4, h-2)
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
		case key.Matches(msg, keys.PriorityDown):
			return c.adjustPriority(1)
		}
	}
	c.list, cmd = c.list.Update(msg)
	return cmd
}

// View renders the column with a border that reflects focus state.
// The header shows "Title (count)" or "Title (count/limit)" when a WIP limit is set.
// When the column is collapsed (0 cards), it renders as a narrow vertical strip instead.
func (c *column) View() string {
	if c.collapsed {
		return c.collapsedView()
	}

	count := len(c.list.Items())
	var header string
	if c.wipLimit > 0 {
		header = fmt.Sprintf("%s (%d/%d)", columnTitles[c.index], count, c.wipLimit)
	} else {
		header = fmt.Sprintf("%s (%d)", columnTitles[c.index], count)
	}

	// Append sort direction icon for the Done column.
	if c.index == colDone {
		if c.sortReversed {
			header += styleAccent().Render(" " + iconSortAsc)
		} else {
			header += styleFaint().Render(" " + iconSortDesc)
		}
	}

	if c.confirmDelete {
		c.list.Title = "Delete? d/esc"
		view := c.getStyle().Render(c.list.View())
		c.list.Title = header
		return view
	}

	saved := c.list.Title
	c.list.Title = header
	view := c.getStyle().Render(c.list.View())
	c.list.Title = saved
	return view
}

// collapsedView renders a narrow vertical strip for an empty column.
// The strip shows the full column title stacked vertically (one character per row)
// so the complete name is readable even at minimal width.
func (c *column) collapsedView() string {
	title := columnTitles[c.index]

	// Stack each character on its own row to read vertically.
	var rows []string
	for _, ch := range title {
		rows = append(rows, string(ch))
	}

	// Pad with spaces to fill available height so the border reaches the bottom.
	for len(rows) < c.height-2 {
		rows = append(rows, " ")
	}

	// Join the character rows with newlines.
	content := ""
	for i, row := range rows {
		if i > 0 {
			content += "\n"
		}
		content += row
	}

	style := c.getCollapsedStyle()
	return style.Render(content)
}

// getCollapsedStyle returns a 1-char-wide bordered style for the collapsed strip.
// Uses a faint border to distinguish it visually from the focused column border.
func (c *column) getCollapsedStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.HiddenBorder()).
		Padding(0, 0).
		Width(collapsedInnerWidth).
		Height(c.height).
		Align(lipgloss.Center)
}

// ViewVertical renders the column as a full-width horizontal band for vertical layout mode.
// Cards are shown as compact single-line rows so all columns stack neatly top-to-bottom.
// The focused column gets a rounded border; others get a hidden border for alignment.
func (c *column) ViewVertical(termWidth int) string {
	count := len(c.list.Items())
	var header string
	if c.wipLimit > 0 {
		header = fmt.Sprintf("%s (%d/%d)", columnTitles[c.index], count, c.wipLimit)
	} else {
		header = fmt.Sprintf("%s (%d)", columnTitles[c.index], count)
	}

	// Append sort direction icon for the Done column.
	if c.index == colDone {
		if c.sortReversed {
			header += styleAccent().Render(" " + iconSortAsc)
		} else {
			header += styleFaint().Render(" " + iconSortDesc)
		}
	}

	// Style the header line — focused column gets a highlighted title.
	var headerStyle lipgloss.Style
	if c.focus {
		headerStyle = styleAccent()
	} else {
		headerStyle = styleFaint()
	}
	renderedHeader := headerStyle.Render(header)

	// Render each card as a compact single line: "  > title [P0]" or "    title [P1]"
	items := c.list.Items()
	selectedIdx := c.list.Index()
	var cardLines []string
	for i, item := range items {
		cd, ok := item.(card)
		if !ok {
			continue
		}

		// Truncate title to leave room for priority tag and cursor prefix.
		// termWidth - border(2) - padding(2) - cursor(2) - priority(5) - spaces(2)
		maxTitle := termWidth - 13
		if maxTitle < 10 {
			maxTitle = 10
		}
		title := truncateTitleForWidth(cd.issue.Title, maxTitle)

		priorityTag := fmt.Sprintf("[P%d]", cd.issue.Priority)
		line := fmt.Sprintf("  %-*s %s", maxTitle, title, priorityTag)

		var lineStyle lipgloss.Style
		if c.focus && i == selectedIdx {
			// Focused selected card: highlighted
			lineStyle = styleAccent()
			line = "> " + line[2:] // replace leading spaces with cursor
		} else if cd.blocked {
			lineStyle = styleFaint()
		} else {
			lineStyle = lipgloss.NewStyle()
		}

		cardLines = append(cardLines, lineStyle.Render(line))
	}

	if len(cardLines) == 0 {
		cardLines = append(cardLines, styleFaint().Render("  (empty)"))
	}

	body := lipgloss.JoinVertical(lipgloss.Left, append([]string{renderedHeader}, cardLines...)...)

	// Apply border: focused uses rounded, blurred uses hidden (same width for alignment).
	const borderWidth = 2
	innerWidth := termWidth - borderWidth
	if innerWidth < 1 {
		innerWidth = 1
	}

	var borderStyle lipgloss.Style
	if c.focus {
		borderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Width(innerWidth)
	} else {
		borderStyle = lipgloss.NewStyle().
			Border(lipgloss.HiddenBorder()).
			Width(innerWidth)
	}

	return borderStyle.Render(body)
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

// Age visualization

// ageBucket classifies how long a card has been in its current column.
type ageBucket int

const (
	ageFresh ageBucket = iota // < 1 day: normal rendering
	ageAging                  // 1–3 days: amber/yellow tint
	ageStale                  // 3+ days: orange-red tint
)

// cardAgeBucket computes the age bucket from a card's UpdatedAt timestamp.
// The caller is responsible for excluding Done column cards before calling.
func cardAgeBucket(updatedAt time.Time) ageBucket {
	age := time.Since(updatedAt)
	switch {
	case age >= 3*24*time.Hour:
		return ageStale
	case age >= 24*time.Hour:
		return ageAging
	default:
		return ageFresh
	}
}

var (
	// agingTitleColor is the theme warning color — amber/yellow for cards aged 1–3 days.
	agingTitleColor = colorWarning
	// staleTitleColor is the theme stale color — orange-red for cards aged 3+ days.
	staleTitleColor = colorStale
)

// ellipsis is the three-dot suffix appended to truncated card titles.
// ASCII dots are used instead of the unicode "…" glyph to stay
// monospace-safe across all terminal fonts.
const ellipsis = "..."

// renderedCard wraps a card to override the Title() returned to the delegate,
// allowing title truncation at render time without mutating the underlying data.
// FilterValue() still returns the original title for search purposes.
type renderedCard struct {
	card
	truncatedTitle string
}

func (r renderedCard) Title() string { return r.truncatedTitle }

// truncateTitleForWidth returns a title truncated to fit within maxCols display
// columns. If the title fits, it is returned unchanged. If it needs truncation,
// it is cut to (maxCols - len(ellipsis)) columns and the ellipsis is appended.
// ansi.Truncate is used so ANSI escape sequences and wide characters are handled
// correctly.
func truncateTitleForWidth(title string, maxCols int) string {
	if maxCols <= 0 {
		return title
	}
	width := ansi.StringWidth(title)
	if width <= maxCols {
		return title
	}
	cutWidth := maxCols - len(ellipsis)
	if cutWidth <= 0 {
		return ellipsis[:maxCols]
	}
	return ansi.Truncate(title, cutWidth, "") + ellipsis
}

// ageAwareDelegate wraps list.DefaultDelegate and overrides title color
// per-item based on how long each card has sat in its column.
type ageAwareDelegate struct {
	list.DefaultDelegate
}

// Render prints the item with age-based title color tint and blocked dimming.
// Blocked cards are rendered faint/dim to signal they cannot be acted on.
// Fresh unblocked cards use the delegate's built-in styles unchanged.
// Aging/stale cards have their title foreground overridden while preserving
// all other style properties (padding, border, selection indicator).
//
// Title truncation is applied here so the ellipsis is always "..." (three ASCII
// dots) rather than the "…" unicode glyph that DefaultDelegate.Render appends.
// The item is wrapped in renderedCard so the truncated title is what
// DefaultDelegate.Render receives — the original title is left untouched for
// filter/search purposes.
func (d ageAwareDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	cd, ok := item.(card)
	if !ok {
		// Fall back to default rendering for non-card items.
		d.DefaultDelegate.Render(w, m, index, item)
		return
	}

	// Clone the delegate so we can mutate styles per-item without affecting
	// the shared delegate state across multiple Render calls.
	local := d.DefaultDelegate

	// Blocked cards: apply faint/dim styling so they visually recede.
	// This is layered on top of any age tinting applied below.
	if cd.blocked {
		local.Styles.NormalTitle = local.Styles.NormalTitle.Faint(true)
		local.Styles.NormalDesc = local.Styles.NormalDesc.Faint(true)
		local.Styles.SelectedTitle = local.Styles.SelectedTitle.Faint(true)
		local.Styles.SelectedDesc = local.Styles.SelectedDesc.Faint(true)
		local.Styles.DimmedTitle = local.Styles.DimmedTitle.Faint(true)
		local.Styles.DimmedDesc = local.Styles.DimmedDesc.Faint(true)
	}

	// Pre-truncate the title so DefaultDelegate.Render won't need to clip it,
	// and so our "..." suffix is used instead of the default "…" glyph.
	textwidth := m.Width() - local.Styles.NormalTitle.GetPaddingLeft() - local.Styles.NormalTitle.GetPaddingRight()
	truncated := truncateTitleForWidth(cd.issue.Title, textwidth)
	renderItem := renderedCard{card: cd, truncatedTitle: truncated}

	bucket := cardAgeBucket(cd.issue.UpdatedAt)
	if bucket == ageFresh {
		local.Render(w, m, index, renderItem)
		return
	}

	var tintColor color.Color
	switch bucket {
	case ageAging:
		tintColor = agingTitleColor
	case ageStale:
		tintColor = staleTitleColor
	}

	local.Styles.NormalTitle = local.Styles.NormalTitle.Foreground(tintColor)
	local.Styles.SelectedTitle = local.Styles.SelectedTitle.Foreground(tintColor)
	local.Styles.DimmedTitle = local.Styles.DimmedTitle.Foreground(tintColor)
	local.Render(w, m, index, renderItem)
}

func newFocusedAgeDelegate() ageAwareDelegate {
	return ageAwareDelegate{DefaultDelegate: newFocusedDelegate()}
}

func newBlurredAgeDelegate() ageAwareDelegate {
	return ageAwareDelegate{DefaultDelegate: newBlurredDelegate()}
}

// truncatingDelegate wraps list.DefaultDelegate and pre-truncates card titles
// with "..." before rendering. Used for columns (e.g. Done) that don't need
// age-based tinting but still need consistent ellipsis style.
type truncatingDelegate struct {
	list.DefaultDelegate
}

// Render pre-truncates the card title with "..." then delegates to DefaultDelegate.
func (d truncatingDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	cd, ok := item.(card)
	if !ok {
		d.DefaultDelegate.Render(w, m, index, item)
		return
	}
	textwidth := m.Width() - d.Styles.NormalTitle.GetPaddingLeft() - d.Styles.NormalTitle.GetPaddingRight()
	truncated := truncateTitleForWidth(cd.issue.Title, textwidth)
	d.DefaultDelegate.Render(w, m, index, renderedCard{card: cd, truncatedTitle: truncated})
}

func newFocusedTruncatingDelegate() truncatingDelegate {
	return truncatingDelegate{DefaultDelegate: newFocusedDelegate()}
}

func newBlurredTruncatingDelegate() truncatingDelegate {
	return truncatingDelegate{DefaultDelegate: newBlurredDelegate()}
}

// Styling

var (
	focusedBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
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
		Foreground(colorAccent).
		BorderLeftForeground(colorAccent)
	d.Styles.SelectedDesc = d.Styles.SelectedDesc.
		Foreground(colorAccent).
		BorderLeftForeground(colorAccent)
	return d
}

func newBlurredDelegate() list.DefaultDelegate {
	d := list.NewDefaultDelegate()
	d.Styles.SelectedTitle = d.Styles.NormalTitle
	d.Styles.SelectedDesc = d.Styles.NormalDesc
	return d
}
