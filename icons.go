package main

// Nerd font icons and special characters used throughout the UI.
// Centralising them here makes it trivial to swap a glyph without
// hunting through every file that renders cards or overlays.

const (
	// Card type icons — shown on the description line of each card.
	iconTask    = "" // Bookmark
	iconBug     = "󰃤" // nf-md-bug
	iconFeature = "󰙴" // nf-md-star_circle
	iconEpic    = "󱐌" // Lightning Bolt

	// iconLock prefixes blocked cards so the constraint is visible at a glance.
	iconLock = "󰌾" // nf-md-lock

	// Sort direction indicators for the Done column header.
	iconSortAsc  = "\U000F0CBD" // nf-md-sort-ascending
	iconSortDesc = "\U000F0CBC" // nf-md-sort-descending

	// Selector arrows used in the form, resolution picker, and any cycle widget.
	iconSelectorLeft  = "◀"
	iconSelectorRight = "▶"
)
