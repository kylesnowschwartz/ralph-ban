package main

import (
	"encoding/json"
	"io"

	beadslite "github.com/kylesnowschwartz/beads-lite"
)

// dumpColumn is the JSON representation of a single kanban column.
type dumpColumn struct {
	Title string     `json:"title"`
	Cards []dumpCard `json:"cards"`
}

// dumpCard is the JSON representation of a single card.
type dumpCard struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Priority int    `json:"priority"`
	Type     string `json:"type"`
}

// dumpOutput is the complete snapshot: structured board state plus rendered view.
type dumpOutput struct {
	View      string       `json:"view"`
	Width     int          `json:"width"`
	Height    int          `json:"height"`
	Focus     int          `json:"focus"`
	PanOffset int          `json:"pan_offset"`
	Columns   []dumpColumn `json:"columns"`
}

// dumpBoard renders one frame of the board and writes JSONL to w.
// Loads issues synchronously, simulates terminal dimensions, and produces
// both the rendered View() text and structured column/card data.
func dumpBoard(store *beadslite.Store, width, height int, w io.Writer) error {
	b := newBoard(store)

	issues, err := store.ListIssues()
	if err != nil {
		return err
	}

	// Simulate initialization that normally happens via tea messages
	b.termWidth = width
	b.termHeight = height
	b.help.Width = width
	b.loaded = true
	b.applyRefresh(issues)
	b.cols[b.focused].Focus()
	b.updatePan()
	b.resizeColumns()

	columns := buildDumpColumns(b)

	out := dumpOutput{
		View:      b.View(),
		Width:     width,
		Height:    height,
		Focus:     int(b.focused),
		PanOffset: b.panOffset,
		Columns:   columns,
	}

	return json.NewEncoder(w).Encode(out)
}

func buildDumpColumns(b *board) []dumpColumn {
	columns := make([]dumpColumn, numColumns)
	for i := columnIndex(0); i < numColumns; i++ {
		cards := []dumpCard{}
		for _, item := range b.cols[i].list.Items() {
			if c, ok := item.(card); ok {
				cards = append(cards, dumpCard{
					ID:       c.issue.ID,
					Title:    c.issue.Title,
					Status:   string(c.issue.Status),
					Priority: c.issue.Priority,
					Type:     string(c.issue.Type),
				})
			}
		}
		columns[i] = dumpColumn{
			Title: columnTitles[i],
			Cards: cards,
		}
	}
	return columns
}
