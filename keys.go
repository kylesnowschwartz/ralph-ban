package main

import "github.com/charmbracelet/bubbles/key"

type keyMap struct {
	Up         key.Binding
	Down       key.Binding
	Left       key.Binding
	Right      key.Binding
	New        key.Binding
	Edit       key.Binding
	Delete     key.Binding
	MoveRight  key.Binding
	MoveLeft   key.Binding
	Undo       key.Binding
	Detail     key.Binding
	PriorityUp key.Binding
	PriorityDn key.Binding
	Help       key.Binding
	Quit       key.Binding
	Back       key.Binding
}

var keys = keyMap{
	Up: key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("k/up", "up"),
	),
	Down: key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("j/down", "down"),
	),
	Left: key.NewBinding(
		key.WithKeys("left", "h"),
		key.WithHelp("h/left", "left"),
	),
	Right: key.NewBinding(
		key.WithKeys("right", "l"),
		key.WithHelp("l/right", "right"),
	),
	New: key.NewBinding(
		key.WithKeys("n"),
		key.WithHelp("n", " new"),
	),
	Edit: key.NewBinding(
		key.WithKeys("e"),
		key.WithHelp("e", "󰏫 edit"),
	),
	Delete: key.NewBinding(
		key.WithKeys("d"),
		key.WithHelp("d", "󰆴 delete"),
	),
	MoveRight: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("⏎", "󰁔 move right"),
	),
	MoveLeft: key.NewBinding(
		key.WithKeys("backspace"),
		key.WithHelp("⌫", "󰁍 move left"),
	),
	Undo: key.NewBinding(
		key.WithKeys("u"),
		key.WithHelp("u", "󰕌 undo"),
	),
	Detail: key.NewBinding(
		key.WithKeys(" "),
		key.WithHelp("space", "󰋼 detail"),
	),
	PriorityUp: key.NewBinding(
		key.WithKeys("+", "="),
		key.WithHelp("+", "󰁞 priority up"),
	),
	PriorityDn: key.NewBinding(
		key.WithKeys("-"),
		key.WithHelp("-", "󰁆 priority down"),
	),
	Help: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "󰋖 help"),
	),
	Quit: key.NewBinding(
		key.WithKeys("ctrl+c"),
		key.WithHelp("C-c", "󰈆 quit"),
	),
	Back: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "󰜺 back"),
	),
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.New, k.MoveRight, k.MoveLeft, k.Undo, k.Help, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Left, k.Right},
		{k.New, k.Edit, k.Delete, k.MoveRight, k.MoveLeft, k.Undo, k.Detail, k.PriorityUp, k.PriorityDn},
		{k.Help, k.Quit, k.Back},
	}
}
