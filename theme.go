package main

import "charm.land/lipgloss/v2"

// Semantic color palette.
// Names reflect what the color communicates, not its numeric value,
// so changing a hue only requires editing here.
var (
	colorBorder  = lipgloss.Color("62")  // panel borders (form, resolution, deplink, focused column)
	colorAccent  = lipgloss.Color("170") // highlights: active fields, focused selection, sort icon active
	colorError   = lipgloss.Color("196") // error text
	colorStale   = lipgloss.Color("202") // card title tint for cards aged 3+ days
	colorZoom    = lipgloss.Color("212") // zoom overlay title and border
	colorWarning = lipgloss.Color("214") // card title tint for cards aged 1–3 days; filter active highlight

	// Card type icon colors — each type gets a distinct color so the icon
	// communicates type at a glance, without reading the label.
	colorIconTask    = lipgloss.Color("35")  // green
	colorIconBug     = lipgloss.Color("214") // orange
	colorIconFeature = lipgloss.Color("35")  // green (same as task — both are "work")
	colorIconEpic    = lipgloss.Color("135") // purple
	colorIconLock    = lipgloss.Color("243") // grey — subdued, informational
)

// Style builders.
// Each call returns a fresh lipgloss.Style — reusing a single var risks
// accumulating mutations across call sites since lipgloss styles are value
// types that copy on assignment but share method-chained state.

// styleFaint returns a dimmed style for secondary/disabled text.
func styleFaint() lipgloss.Style {
	return lipgloss.NewStyle().Faint(true)
}

// styleBold returns a bold style for emphasis.
func styleBold() lipgloss.Style {
	return lipgloss.NewStyle().Bold(true)
}

// styleAccent returns the accent foreground with bold weight.
// Used for active labels, focused card selections, and sort indicators.
func styleAccent() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
}

// styleError returns the error foreground style.
func styleError() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(colorError)
}

// stylePanelBorder returns the standard rounded-border panel style.
// All modal overlays (form, resolution picker, deplink, zoom) share this base.
func stylePanelBorder() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		Padding(1, 2)
}
