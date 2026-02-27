package main

import "github.com/charmbracelet/bubbles/key"

type keyMap struct {
	Up           key.Binding
	Down         key.Binding
	Left         key.Binding
	Right        key.Binding
	New          key.Binding
	Edit         key.Binding
	Delete       key.Binding
	MoveRight    key.Binding
	MoveLeft     key.Binding
	Undo         key.Binding
	Detail       key.Binding
	Zoom         key.Binding // peek at the focused card's full content; any key dismisses
	PriorityUp   key.Binding
	PriorityDown key.Binding
	Search       key.Binding
	FilterNext   key.Binding
	FilterPrev   key.Binding
	BlockedBy    key.Binding // open dep-link picker: focused card is blocked by selection
	Blocks       key.Binding // open dep-link picker: focused card blocks selection
	Help         key.Binding
	Quit         key.Binding
	Suspend      key.Binding
	Back         key.Binding
	CtrlClick    key.Binding // display-only: mouse events bypass key bindings
	LayoutToggle key.Binding // switch between horizontal and vertical board layout
}

var keys = keyMap{
	Up: key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("k/↑", "up"),
	),
	Down: key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("j/↓", "down"),
	),
	Left: key.NewBinding(
		key.WithKeys("left", "h"),
		key.WithHelp("h/←", "left"),
	),
	Right: key.NewBinding(
		key.WithKeys("right", "l"),
		key.WithHelp("l/→", "right"),
	),
	New: key.NewBinding(
		key.WithKeys("n"),
		key.WithHelp("n", "new"),
	),
	Edit: key.NewBinding(
		key.WithKeys("e"),
		key.WithHelp("e", "edit"),
	),
	Delete: key.NewBinding(
		key.WithKeys("d"),
		key.WithHelp("d", "delete"),
	),
	MoveRight: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("⏎", "move →"),
	),
	MoveLeft: key.NewBinding(
		key.WithKeys("backspace"),
		key.WithHelp("⌫", "move ←"),
	),
	Undo: key.NewBinding(
		key.WithKeys("u"),
		key.WithHelp("u", "undo"),
	),
	Detail: key.NewBinding(
		key.WithKeys(" "),
		key.WithHelp("space", "detail"),
	),
	Zoom: key.NewBinding(
		key.WithKeys("z"),
		key.WithHelp("z", "peek"),
	),
	PriorityUp: key.NewBinding(
		key.WithKeys("+", "="),
		key.WithHelp("+", "pri ↑"),
	),
	PriorityDown: key.NewBinding(
		key.WithKeys("-"),
		key.WithHelp("-", "pri ↓"),
	),
	Search: key.NewBinding(
		key.WithKeys("/"),
		key.WithHelp("/", "search"),
	),
	FilterNext: key.NewBinding(
		key.WithKeys("f"),
		key.WithHelp("f", "filter →"),
	),
	FilterPrev: key.NewBinding(
		key.WithKeys("F"),
		key.WithHelp("F", "filter ←"),
	),
	BlockedBy: key.NewBinding(
		key.WithKeys("b"),
		key.WithHelp("b", "blocked by"),
	),
	Blocks: key.NewBinding(
		key.WithKeys("B"),
		key.WithHelp("B", "blocks"),
	),
	Help: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "more"),
	),
	Quit: key.NewBinding(
		key.WithKeys("ctrl+c"),
		key.WithHelp("C-c", "quit"),
	),
	Suspend: key.NewBinding(
		key.WithKeys("ctrl+z"),
		key.WithHelp("C-z", "suspend"),
	),
	Back: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "back"),
	),
	CtrlClick: key.NewBinding(
		key.WithHelp("ctrl+click", "move to column"),
	),
	LayoutToggle: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "toggle layout"),
	),
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.New, k.Edit, k.MoveRight, k.MoveLeft, k.Search, k.FilterNext, k.LayoutToggle, k.Help}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Left, k.Right},
		{k.New, k.Edit, k.Delete, k.MoveRight, k.MoveLeft, k.Undo, k.Detail, k.Zoom, k.PriorityUp, k.PriorityDown},
		{k.Search, k.FilterNext, k.FilterPrev, k.BlockedBy, k.Blocks, k.LayoutToggle, k.Help, k.Quit, k.Suspend, k.Back, k.CtrlClick},
	}
}
