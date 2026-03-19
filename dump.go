package main

import (
	"encoding/json"
	"fmt"
	"io"

	beadslite "github.com/kylesnowschwartz/beads-lite"
)

// exportCard is the JSON representation of a single card in any export path.
// Assignee is omitted when empty (dump doesn't need it, snapshot does).
type exportCard struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Priority int    `json:"priority"`
	Type     string `json:"type"`
	Assignee string `json:"assignee,omitempty"`
}

// exportColumn is the JSON representation of a single kanban column.
// WIP is omitted when zero (dump doesn't need it, snapshot does).
type exportColumn struct {
	Title string       `json:"title"`
	WIP   int          `json:"wip,omitempty"`
	Cards []exportCard `json:"cards"`
}

// newBoardForExport constructs a board from the store and initializes it
// for off-screen rendering at the given dimensions. Shared by dumpBoard
// and writeSnapshotASCII — both need the same 8-step init sequence.
func newBoardForExport(s *beadslite.Store, width, height int) (*board, error) {
	b := newBoard(s)
	msg := fetchRefresh(s)
	rm, ok := msg.(refreshMsg)
	if !ok {
		if e, isErr := msg.(errMsg); isErr {
			return nil, e.err
		}
		return nil, fmt.Errorf("unexpected message type: %T", msg)
	}
	b.termWidth = width
	b.termHeight = height
	b.help.SetWidth(width)
	b.loaded = true
	b.applyRefresh(rm)
	b.cols[b.focused].Focus()
	b.updatePan()
	b.resizeColumns()
	return b, nil
}

// buildExportColumns extracts structured column/card data from an initialized board.
// Fields that are zero-valued (WIP=0, Assignee="") are omitted by json omitempty.
func buildExportColumns(b *board) []exportColumn {
	columns := make([]exportColumn, numColumns)
	for i := columnIndex(0); i < numColumns; i++ {
		cards := []exportCard{}
		for _, item := range b.cols[i].list.Items() {
			if c, ok := item.(card); ok {
				cards = append(cards, exportCard{
					ID:       c.issue.ID,
					Title:    c.issue.Title,
					Status:   string(c.issue.Status),
					Priority: c.issue.Priority,
					Type:     string(c.issue.Type),
					Assignee: c.issue.AssignedTo,
				})
			}
		}
		columns[i] = exportColumn{
			Title: columnTitles[i],
			WIP:   len(cards),
			Cards: cards,
		}
	}
	return columns
}

// dumpOutput is the complete snapshot: structured board state plus rendered view.
type dumpOutput struct {
	View      string         `json:"view"`
	Width     int            `json:"width"`
	Height    int            `json:"height"`
	Focus     int            `json:"focus"`
	PanOffset int            `json:"pan_offset"`
	Columns   []exportColumn `json:"columns"`
}

// dumpBoard renders one frame of the board and writes JSONL to w.
// Loads issues synchronously, simulates terminal dimensions, and produces
// both the rendered View() text and structured column/card data.
func dumpBoard(store *beadslite.Store, width, height int, w io.Writer) error {
	b, err := newBoardForExport(store, width, height)
	if err != nil {
		return err
	}

	out := dumpOutput{
		View:      b.viewContent(),
		Width:     width,
		Height:    height,
		Focus:     int(b.focused),
		PanOffset: b.panOffset,
		Columns:   buildExportColumns(b),
	}

	return json.NewEncoder(w).Encode(out)
}

// dumpZoomView renders the zoom overlay for a specific card and writes
// JSON with the rendered view to w. Finds the card by ID across all columns,
// focuses it, opens zoom, and renders one frame.
func dumpZoomView(store *beadslite.Store, cardID string, width, height int, w io.Writer) error {
	b, err := newBoardForExport(store, width, height)
	if err != nil {
		return err
	}

	// Find and focus the card.
	found := false
	for col := columnIndex(0); col < numColumns; col++ {
		for idx, item := range b.cols[col].list.Items() {
			if c, ok := item.(card); ok && c.issue.ID == cardID {
				b.cols[b.focused].Blur()
				b.focused = col
				b.cols[col].Focus()
				b.cols[col].list.Select(idx)
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		return fmt.Errorf("card %q not found", cardID)
	}

	b.openZoom()

	out := dumpOutput{
		View:   b.zoomView(),
		Width:  width,
		Height: height,
		Focus:  int(b.focused),
	}

	return json.NewEncoder(w).Encode(out)
}
