package main

import (
	"testing"

	beadslite "github.com/kylesnowschwartz/beads-lite"
)

func TestColumnToStatusMapping(t *testing.T) {
	// Every column index maps to a valid beads-lite status
	expected := map[columnIndex]beadslite.Status{
		colBacklog: beadslite.StatusBacklog,
		colTodo:    beadslite.StatusTodo,
		colDoing:   beadslite.StatusDoing,
		colReview:  beadslite.StatusReview,
		colDone:    beadslite.StatusDone,
	}

	for idx, want := range expected {
		got := columnToStatus[idx]
		if got != want {
			t.Errorf("columnToStatus[%d] = %q, want %q", idx, got, want)
		}
	}
}

func TestStatusToColumnMapping(t *testing.T) {
	// Every beads-lite status maps back to the correct column index
	expected := map[beadslite.Status]columnIndex{
		beadslite.StatusBacklog: colBacklog,
		beadslite.StatusTodo:    colTodo,
		beadslite.StatusDoing:   colDoing,
		beadslite.StatusReview:  colReview,
		beadslite.StatusDone:    colDone,
	}

	for status, want := range expected {
		got, ok := statusToColumn[status]
		if !ok {
			t.Errorf("statusToColumn[%q] not found", status)
			continue
		}
		if got != want {
			t.Errorf("statusToColumn[%q] = %d, want %d", status, got, want)
		}
	}
}

func TestMappingsAreInverse(t *testing.T) {
	// columnToStatus and statusToColumn must be exact inverses
	for i := columnIndex(0); i < numColumns; i++ {
		status := columnToStatus[i]
		col, ok := statusToColumn[status]
		if !ok {
			t.Errorf("status %q from column %d has no reverse mapping", status, i)
			continue
		}
		if col != i {
			t.Errorf("round-trip column %d -> status %q -> column %d", i, status, col)
		}
	}
}

func TestColumnTitles(t *testing.T) {
	expected := [numColumns]string{
		"Backlog", "To Do", "Doing", "Review", "Done",
	}
	for i := columnIndex(0); i < numColumns; i++ {
		if columnTitles[i] != expected[i] {
			t.Errorf("columnTitles[%d] = %q, want %q", i, columnTitles[i], expected[i])
		}
	}
}

func TestNumColumns(t *testing.T) {
	if numColumns != 5 {
		t.Errorf("numColumns = %d, want 5", numColumns)
	}
}

func TestConfirmDeleteResetsOnBlur(t *testing.T) {
	c := newColumn(colTodo)
	c.Focus()
	c.confirmDelete = true

	c.Blur()

	if c.confirmDelete {
		t.Error("Blur() should reset confirmDelete to false")
	}
	if c.focus {
		t.Error("Blur() should set focus to false")
	}
}
