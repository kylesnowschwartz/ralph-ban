package main

import (
	"fmt"

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
// Tab cycles between title, description, priority, and type fields.
// Description is a textarea (Enter inserts newlines); other fields use Enter to submit.
// Priority and type are selectors: left/right cycles options.
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
}

func newTextarea() textarea.Model {
	ta := textarea.New()
	ta.Placeholder = "Description (optional)..."
	ta.SetWidth(40)
	ta.SetHeight(4)
	ta.CharLimit = 2000
	ta.ShowLineNumbers = false

	// The default CursorLine uses Background("0") which renders as a black bar
	// on light terminals where dark/light detection fails. Clear just the
	// background; leave all other textarea styles at their defaults.
	styles := textarea.DefaultStyles(true)
	styles.Focused.CursorLine = styles.Focused.CursorLine.UnsetBackground()
	ta.SetStyles(styles)

	return ta
}

func newForm(colIdx columnIndex) form {
	ti := textinput.New()
	ti.Placeholder = "Card title..."
	ti.Focus()
	ti.CharLimit = 120
	ti.SetWidth(40)

	return form{
		title:       ti,
		description: newTextarea(),
		priority:    2, // P2 medium
		typeIndex:   0, // task
		focus:       fieldTitle,
		columnIndex: colIdx,
	}
}

func editForm(issue *beadslite.Issue, colIdx columnIndex) form {
	ti := textinput.New()
	ti.SetValue(issue.Title)
	ti.Focus()
	ti.CharLimit = 120
	ti.SetWidth(40)

	ta := newTextarea()
	ta.SetValue(issue.Description)

	typeIdx := 0
	for i, t := range typeOptions {
		if t == issue.Type {
			typeIdx = i
			break
		}
	}

	return form{
		title:       ti,
		description: ta,
		priority:    issue.Priority,
		typeIndex:   typeIdx,
		focus:       fieldTitle,
		editing:     issue,
		columnIndex: colIdx,
	}
}

func (f form) Update(msg tea.Msg) (form, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, keys.Back):
			return f, nil

		case msg.String() == "tab":
			f.advanceFocus(1)
			return f, nil

		case msg.String() == "shift+tab":
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

	style := stylePanelBorder().Width(50)

	label := lipgloss.NewStyle().Width(10)
	active := lipgloss.NewStyle().Foreground(colorAccent)
	faint := styleFaint()

	// Title row
	titleLabel := label.Render("Title:")
	if f.focus == fieldTitle {
		titleLabel = active.Width(10).Render("Title:")
	}
	titleRow := titleLabel + " " + f.title.View()

	// Description row
	descLabel := label.Render("Desc:")
	if f.focus == fieldDescription {
		descLabel = active.Width(10).Render("Desc:")
	}
	descRow := descLabel + "\n" + f.description.View()

	// Priority row
	priLabel := label.Render("Priority:")
	priValue := priorityLabels[f.priority]
	if f.focus == fieldPriority {
		priLabel = active.Width(10).Render("Priority:")
		priValue = fmt.Sprintf("%s %s %s", iconSelectorLeft, priValue, iconSelectorRight)
	}
	priRow := priLabel + " " + priValue

	// Type row
	typeLabel := label.Render("Type:")
	typeValue := string(typeOptions[f.typeIndex])
	if f.focus == fieldType {
		typeLabel = active.Width(10).Render("Type:")
		typeValue = fmt.Sprintf("%s %s %s", iconSelectorLeft, typeValue, iconSelectorRight)
	}
	typeRow := typeLabel + " " + typeValue

	// Footer hint adapts to current field.
	hint := "tab: next  enter: save  esc: cancel"
	if f.focus == fieldDescription {
		hint = "tab: next  esc: cancel"
	}

	content := lipgloss.JoinVertical(lipgloss.Left,
		styleBold().Render(header),
		"",
		titleRow,
		descRow,
		priRow,
		typeRow,
		"",
		faint.Render(hint),
	)

	rendered := style.Render(content)

	return lipgloss.Place(f.width, f.height,
		lipgloss.Center, lipgloss.Center,
		rendered,
	)
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

	return func() tea.Msg {
		if f.editing != nil {
			f.editing.Title = title
			f.editing.Description = desc
			f.editing.Priority = priority
			f.editing.Type = issueType
			return saveMsg{issue: f.editing}
		}
		issue := beadslite.NewIssue(title)
		issue.Status = columnToStatus[f.columnIndex]
		issue.Description = desc
		issue.Priority = priority
		issue.Type = issueType
		return saveMsg{issue: issue}
	}
}
