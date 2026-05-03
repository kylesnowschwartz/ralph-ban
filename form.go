package main

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	beadslite "github.com/kylesnowschwartz/beads-lite"
)

// formField identifies which field has focus in the form.
type formField int

const (
	fieldTitle formField = iota
	fieldDescription
	fieldPriority
	fieldType
	fieldSpecs
	numFormFields
)

// Priority labels displayed in the selector.
var priorityLabels = [5]string{
	"P0 critical",
	"P1 high",
	"P2 medium",
	"P3 low",
	"P4 lowest",
}

// Issue type options for the selector.
var typeOptions = []beadslite.IssueType{
	beadslite.IssueTypeTask,
	beadslite.IssueTypeBug,
	beadslite.IssueTypeFeature,
	beadslite.IssueTypeEpic,
}

// form is a modal overlay for creating or editing a card.
// Tab cycles between title, description, priority, type, and specs fields.
// Description is a textarea (Enter inserts newlines); other fields use Enter to submit.
// Priority and type are selectors: left/right cycles options.
// Specs is a toggleable checklist: j/k navigates, space toggles, a adds, d removes.
type form struct {
	title       textinput.Model
	description textarea.Model
	priority    int // 0-4
	typeIndex   int // index into typeOptions
	focus       formField
	editing     *beadslite.Issue
	columnIndex columnIndex
	width       int
	height      int

	// Specs state
	specs       []beadslite.Spec
	specIndex   int // cursor within specs list
	specInput   textinput.Model
	addingSpec  bool // true when the text input for a new spec is visible
	editingSpec int  // index of spec being edited, or -1 when adding new

	isDark bool // terminal background brightness; controls component style variants
}

func newTextarea(isDark bool) textarea.Model {
	ta := textarea.New()
	ta.Placeholder = "Description (optional)..."
	ta.SetWidth(40)
	ta.SetHeight(4)
	ta.CharLimit = 2000
	ta.ShowLineNumbers = false
	ta.Prompt = "" // remove the thick left-border "scrollbar" prompt
	ta.SetStyles(textarea.DefaultStyles(isDark))
	return ta
}

func newSpecInput(isDark bool) textinput.Model {
	ti := textinput.New()
	ti.Placeholder = "New specification..."
	ti.CharLimit = 200
	ti.SetWidth(60)
	ti.SetStyles(textinput.DefaultStyles(isDark))
	return ti
}

func newForm(colIdx columnIndex, isDark bool) form {
	ti := textinput.New()
	ti.Placeholder = "Card title..."
	ti.Focus()
	ti.CharLimit = 120
	ti.SetWidth(40)
	ti.SetStyles(textinput.DefaultStyles(isDark))

	return form{
		title:       ti,
		description: newTextarea(isDark),
		priority:    2, // P2 medium
		typeIndex:   0, // task
		focus:       fieldTitle,
		columnIndex: colIdx,
		specInput:   newSpecInput(isDark),
		editingSpec: -1,
		isDark:      isDark,
	}
}

func editForm(issue *beadslite.Issue, colIdx columnIndex, isDark bool) form {
	ti := textinput.New()
	ti.SetValue(issue.Title)
	ti.Focus()
	ti.CharLimit = 120
	ti.SetWidth(40)
	ti.SetStyles(textinput.DefaultStyles(isDark))

	ta := newTextarea(isDark)
	ta.SetValue(issue.Description)
	ta.MoveToBegin() // SetValue leaves cursor at end; start at top

	typeIdx := 0
	for i, t := range typeOptions {
		if t == issue.Type {
			typeIdx = i
			break
		}
	}

	// Copy specs so mutations in the form don't affect the issue until save.
	specs := make([]beadslite.Spec, len(issue.Specifications))
	copy(specs, issue.Specifications)

	return form{
		title:       ti,
		description: ta,
		priority:    issue.Priority,
		typeIndex:   typeIdx,
		focus:       fieldTitle,
		editing:     issue,
		columnIndex: colIdx,
		specs:       specs,
		specInput:   newSpecInput(isDark),
		editingSpec: -1,
		isDark:      isDark,
	}
}

func (f form) Update(msg tea.Msg) (form, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// When adding a spec, the text input captures all keys except enter/esc.
		if f.addingSpec {
			return f.updateSpecInput(msg)
		}

		switch {
		case key.Matches(msg, keys.Back):
			return f, nil

		case msg.String() == "tab", msg.String() == "down":
			// Down arrow navigates fields except inside the description
			// textarea (cursor movement) and specs list (item navigation).
			if msg.String() == "down" && (f.focus == fieldDescription || f.focus == fieldSpecs) {
				break
			}
			f.advanceFocus(1)
			return f, nil

		case msg.String() == "shift+tab", msg.String() == "up":
			if msg.String() == "up" && (f.focus == fieldDescription || f.focus == fieldSpecs) {
				break
			}
			f.advanceFocus(-1)
			return f, nil

		case msg.String() == "enter":
			// In the description textarea, Enter inserts a newline.
			// From all other fields, Enter submits the form.
			if f.focus != fieldDescription {
				return f, f.submit()
			}
		}

		// Selector fields handle left/right to cycle options.
		if f.focus == fieldPriority {
			switch {
			case key.Matches(msg, keys.Left) || msg.String() == "-":
				if f.priority < 4 {
					f.priority++
				}
				return f, nil
			case key.Matches(msg, keys.Right) || msg.String() == "+", msg.String() == "=":
				if f.priority > 0 {
					f.priority--
				}
				return f, nil
			}
		}
		if f.focus == fieldType {
			switch {
			case key.Matches(msg, keys.Left):
				f.typeIndex = (f.typeIndex - 1 + len(typeOptions)) % len(typeOptions)
				return f, nil
			case key.Matches(msg, keys.Right):
				f.typeIndex = (f.typeIndex + 1) % len(typeOptions)
				return f, nil
			}
		}

		// Specs field: navigate, toggle, add, remove.
		if f.focus == fieldSpecs {
			return f.updateSpecs(msg)
		}
	}

	// Forward to the focused text component.
	switch f.focus {
	case fieldTitle:
		var cmd tea.Cmd
		f.title, cmd = f.title.Update(msg)
		return f, cmd
	case fieldDescription:
		var cmd tea.Cmd
		f.description, cmd = f.description.Update(msg)
		return f, cmd
	}
	return f, nil
}

// updateSpecs handles key events when the specs field is focused.
func (f form) updateSpecs(msg tea.KeyMsg) (form, tea.Cmd) {
	switch {
	case msg.String() == "j" || msg.String() == "down":
		if f.specIndex < len(f.specs)-1 {
			f.specIndex++
		}
	case msg.String() == "k" || msg.String() == "up":
		if f.specIndex > 0 {
			f.specIndex--
		}
	case msg.String() == "space" || msg.String() == " ":
		// Toggle the selected spec.
		if f.specIndex >= 0 && f.specIndex < len(f.specs) {
			f.specs[f.specIndex].Checked = !f.specs[f.specIndex].Checked
		}
	case msg.String() == "a":
		// Start adding a new spec.
		f.addingSpec = true
		f.editingSpec = -1
		f.specInput.SetValue("")
		f.specInput.Focus()
	case msg.String() == "e":
		// Edit the selected spec.
		if f.specIndex >= 0 && f.specIndex < len(f.specs) {
			f.addingSpec = true
			f.editingSpec = f.specIndex
			f.specInput.SetValue(f.specs[f.specIndex].Text)
			f.specInput.Focus()
		}
	case msg.String() == "d" || msg.String() == "backspace":
		// Remove the selected spec.
		if f.specIndex >= 0 && f.specIndex < len(f.specs) {
			f.specs = append(f.specs[:f.specIndex], f.specs[f.specIndex+1:]...)
			if f.specIndex >= len(f.specs) && f.specIndex > 0 {
				f.specIndex--
			}
		}
	}
	return f, nil
}

// updateSpecInput handles key events while typing a new or edited spec.
func (f form) updateSpecInput(msg tea.KeyMsg) (form, tea.Cmd) {
	switch {
	case msg.String() == "enter":
		text := strings.TrimSpace(f.specInput.Value())
		if text != "" {
			if f.editingSpec >= 0 && f.editingSpec < len(f.specs) {
				// Replace existing spec text, preserving checked state.
				f.specs[f.editingSpec].Text = text
			} else {
				f.specs = append(f.specs, beadslite.Spec{Text: text})
				f.specIndex = len(f.specs) - 1
			}
		}
		f.addingSpec = false
		f.editingSpec = -1
		f.specInput.Blur()
		return f, nil

	case key.Matches(msg, keys.Back):
		f.addingSpec = false
		f.editingSpec = -1
		f.specInput.Blur()
		return f, nil
	}

	var cmd tea.Cmd
	f.specInput, cmd = f.specInput.Update(msg)
	return f, cmd
}

// advanceFocus moves focus by delta fields, wrapping around.
func (f *form) advanceFocus(delta int) {
	next := (int(f.focus) + delta + int(numFormFields)) % int(numFormFields)
	f.focus = formField(next)

	// Only one text component should be focused at a time.
	f.title.Blur()
	f.description.Blur()

	switch f.focus {
	case fieldTitle:
		f.title.Focus()
	case fieldDescription:
		f.description.Focus()
	}
}

func (f form) View() string {
	header := "New Card"
	if f.editing != nil {
		header = "Edit Card"
	}

	const outerMargin = 8
	panelStyle := stylePanelBorder()
	descBorderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorFaint)
	label := lipgloss.NewStyle().Width(10)

	panelWidth := max(f.width-outerMargin, 50)
	contentWidth := max(panelWidth-panelStyle.GetHorizontalFrameSize(), 40)
	panelVFrame := panelStyle.GetVerticalFrameSize()
	descBorderVFrame := descBorderStyle.GetVerticalFrameSize()

	active := lipgloss.NewStyle().Foreground(colorAccent)
	faint := styleFaint()

	landscape := f.width >= 2*f.height && f.width >= 100

	if landscape {
		return f.viewLandscape(header, panelStyle, descBorderStyle, label,
			panelWidth, contentWidth, panelVFrame, descBorderVFrame, active, faint)
	}
	return f.viewPortrait(header, panelStyle, descBorderStyle, label,
		panelWidth, contentWidth, panelVFrame, descBorderVFrame, active, faint)
}

// viewPortrait renders the single-column form layout for narrow/tall terminals.
func (f form) viewPortrait(
	header string, panelStyle, descBorderStyle, label lipgloss.Style,
	panelWidth, contentWidth, panelVFrame, descBorderVFrame int,
	active, faint lipgloss.Style,
) string {
	descWidth := max(contentWidth-descBorderStyle.GetHorizontalFrameSize(), 38)
	labelWidth := label.GetWidth() + 1
	f.title.SetWidth(max(contentWidth-labelWidth, 20))
	f.description.SetWidth(descWidth)

	fixedRows := 10
	availHeight := f.height - 8 - panelVFrame - descBorderVFrame - fixedRows
	f.description.SetHeight(max(availHeight/2, 4))

	titleRow := f.renderTitleRow(label, active)
	descRow := f.renderDescLabel(label, active) + "\n" + descBorderStyle.Render(f.description.View())
	priRow := f.renderPriorityRow(label, active)
	typeRow := f.renderTypeRow(label, active)
	specsView := f.viewSpecs(label, active, faint)
	hint := f.renderHint(faint)

	content := lipgloss.JoinVertical(lipgloss.Left,
		styleBold().Render(header),
		"",
		titleRow,
		descRow,
		priRow,
		typeRow,
		specsView,
		"",
		hint,
	)

	rendered := panelStyle.Width(panelWidth).Render(content)
	return lipgloss.Place(f.width, f.height,
		lipgloss.Center, lipgloss.Center,
		rendered,
	)
}

// viewLandscape renders a two-column form: metadata on the left, description
// on the right. The description textarea gets the full panel height, giving
// long descriptions room to breathe. Tab order is unchanged: title → desc →
// priority → type → specs.
func (f form) viewLandscape(
	header string, panelStyle, descBorderStyle, label lipgloss.Style,
	panelWidth, contentWidth, panelVFrame, descBorderVFrame int,
	active, faint lipgloss.Style,
) string {
	// Left/right column widths. Left gets 45% for metadata, right gets
	// the remainder minus a 3-char divider.
	dividerWidth := 3
	leftWidth := contentWidth * 45 / 100
	rightWidth := contentWidth - leftWidth - dividerWidth

	labelWidth := label.GetWidth() + 1
	f.title.SetWidth(max(leftWidth-labelWidth, 20))

	// Description textarea fills the full right column height.
	descInnerWidth := max(rightWidth-descBorderStyle.GetHorizontalFrameSize(), 30)
	f.description.SetWidth(descInnerWidth)
	// Description gets the full right column height: panel inner height
	// minus the "Desc:" label line and the border around the textarea.
	panelInnerHeight := f.height - 8 - panelVFrame // 8 = outerMargin for centering
	descAvailHeight := panelInnerHeight - 1 - descBorderVFrame
	f.description.SetHeight(max(descAvailHeight, 6))

	titleRow := f.renderTitleRow(label, active)
	priRow := f.renderPriorityRow(label, active)
	typeRow := f.renderTypeRow(label, active)
	specsView := f.viewSpecs(label, active, faint)
	hint := f.renderHint(faint)

	// Left column: title, metadata, specs, hint.
	left := lipgloss.JoinVertical(lipgloss.Left,
		styleBold().Render(header),
		"",
		titleRow,
		"",
		priRow,
		typeRow,
		specsView,
		"",
		hint,
	)
	left = lipgloss.NewStyle().Width(leftWidth).Render(left)

	// Right column: description label + bordered textarea.
	descLabel := f.renderDescLabel(label, active)
	descContent := descLabel + "\n" + descBorderStyle.Width(rightWidth).Render(f.description.View())
	right := lipgloss.NewStyle().Width(rightWidth).Render(descContent)

	// Divider sized to the taller column.
	maxHeight := max(lipgloss.Height(left), lipgloss.Height(right))
	divLines := make([]string, maxHeight)
	divStyle := styleFaint()
	for i := range divLines {
		divLines[i] = divStyle.Render(" │ ")
	}
	divider := strings.Join(divLines, "\n")

	content := lipgloss.JoinHorizontal(lipgloss.Top, left, divider, right)
	rendered := panelStyle.Width(panelWidth).Render(content)
	return lipgloss.Place(f.width, f.height,
		lipgloss.Center, lipgloss.Center,
		rendered,
	)
}

// Field rendering helpers — shared between portrait and landscape layouts.

func (f form) renderTitleRow(label, active lipgloss.Style) string {
	titleLabel := label.Render("Title:")
	if f.focus == fieldTitle {
		titleLabel = active.Width(label.GetWidth()).Render("Title:")
	}
	return titleLabel + " " + f.title.View()
}

func (f form) renderDescLabel(label, active lipgloss.Style) string {
	descLabel := label.Render("Desc:")
	if f.focus == fieldDescription {
		descLabel = active.Width(label.GetWidth()).Render("Desc:")
	}
	return descLabel
}

func (f form) renderPriorityRow(label, active lipgloss.Style) string {
	priLabel := label.Render("Priority:")
	priValue := priorityLabels[f.priority]
	if f.focus == fieldPriority {
		priLabel = active.Width(label.GetWidth()).Render("Priority:")
		priValue = fmt.Sprintf("%s %s %s", iconSelectorLeft, priValue, iconSelectorRight)
	}
	return priLabel + " " + priValue
}

func (f form) renderTypeRow(label, active lipgloss.Style) string {
	typeLabel := label.Render("Type:")
	typeValue := string(typeOptions[f.typeIndex])
	if f.focus == fieldType {
		typeLabel = active.Width(label.GetWidth()).Render("Type:")
		typeValue = fmt.Sprintf("%s %s %s", iconSelectorLeft, typeValue, iconSelectorRight)
	}
	return typeLabel + " " + typeValue
}

func (f form) renderHint(faint lipgloss.Style) string {
	var hint string
	switch f.focus {
	case fieldDescription:
		hint = "tab: next field  esc: cancel"
	case fieldSpecs:
		if f.addingSpec {
			hint = "enter: add  esc: cancel"
		} else {
			hint = "space: toggle  a: add  e: edit  d: remove  tab: next  enter: save"
		}
	default:
		hint = "↑↓/tab: navigate  enter: save  esc: cancel"
	}
	return faint.Render(hint)
}

// viewSpecs renders the specifications section of the form.
func (f form) viewSpecs(label, active, faint lipgloss.Style) string {
	specLabel := label.Render("Specs:")
	if f.focus == fieldSpecs {
		specLabel = active.Width(label.GetWidth()).Render("Specs:")
	}

	if len(f.specs) == 0 && !f.addingSpec && f.focus != fieldSpecs {
		// Hide specs section entirely when empty and not focused.
		return ""
	}

	var lines []string
	for i, spec := range f.specs {
		// When editing this spec inline, show the text input in place of the text.
		if f.addingSpec && f.editingSpec == i {
			mark := iconSpecUnchecked
			if spec.Checked {
				mark = iconSpecChecked
			}
			lines = append(lines, fmt.Sprintf("  %s %s", mark, f.specInput.View()))
			continue
		}

		mark := iconSpecUnchecked
		if spec.Checked {
			mark = iconSpecChecked
		}
		line := fmt.Sprintf("  %s %s", mark, spec.Text)
		if f.focus == fieldSpecs && i == f.specIndex && !f.addingSpec {
			line = active.Render(line)
		}
		lines = append(lines, line)
	}

	// New spec input appears at the bottom of the list (editingSpec == -1).
	if f.addingSpec && f.editingSpec < 0 {
		lines = append(lines, "  "+f.specInput.View())
	}

	if len(f.specs) == 0 && !f.addingSpec {
		lines = append(lines, faint.Render("  (none - press a to add)"))
	}

	return "\n" + specLabel + "\n" + strings.Join(lines, "\n")
}

// submit creates the appropriate issue and returns a saveMsg.
func (f form) submit() tea.Cmd {
	title := f.title.Value()
	if title == "" {
		return nil
	}

	priority := f.priority
	issueType := typeOptions[f.typeIndex]
	desc := f.description.Value()
	specs := f.specs

	return func() tea.Msg {
		if f.editing != nil {
			f.editing.Title = title
			f.editing.Description = desc
			f.editing.Priority = priority
			f.editing.Type = issueType
			f.editing.Specifications = specs
			return saveMsg{issue: f.editing}
		}
		issue := beadslite.NewIssue(title)
		issue.Status = columnToStatus[f.columnIndex]
		issue.Description = desc
		issue.Priority = priority
		issue.Type = issueType
		issue.Specifications = specs
		return saveMsg{issue: issue}
	}
}
