package main

import (
	"bytes"
	"encoding/json"
	"testing"

	beadslite "github.com/kylesnowschwartz/beads-lite"
)

func TestDumpBoard_EmptyStore(t *testing.T) {
	store := newTestStore(t)
	var buf bytes.Buffer

	if err := dumpBoard(store, 120, 40, &buf); err != nil {
		t.Fatalf("dumpBoard: %v", err)
	}

	var out dumpOutput
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if out.Width != 120 {
		t.Errorf("width = %d, want 120", out.Width)
	}
	if out.Height != 40 {
		t.Errorf("height = %d, want 40", out.Height)
	}
	if len(out.Columns) != int(numColumns) {
		t.Fatalf("columns = %d, want %d", len(out.Columns), numColumns)
	}
	for i, col := range out.Columns {
		if len(col.Cards) != 0 {
			t.Errorf("column %d (%s) has %d cards, want 0", i, col.Title, len(col.Cards))
		}
	}
	if out.View == "" {
		t.Error("view should not be empty")
	}
}

func TestDumpBoard_CardsInCorrectColumns(t *testing.T) {
	store := newTestStore(t)

	issues := []*beadslite.Issue{
		makeIssue("bl-d1", "Backlog Card", beadslite.StatusBacklog),
		makeIssue("bl-d2", "Todo Card", beadslite.StatusTodo),
		makeIssue("bl-d3", "Doing Card", beadslite.StatusDoing),
		makeIssue("bl-d4", "Review Card", beadslite.StatusReview),
		makeIssue("bl-d5", "Done Card", beadslite.StatusDone),
	}
	for _, issue := range issues {
		if err := store.CreateIssue(issue); err != nil {
			t.Fatalf("CreateIssue(%s): %v", issue.ID, err)
		}
	}

	var buf bytes.Buffer
	if err := dumpBoard(store, 120, 40, &buf); err != nil {
		t.Fatalf("dumpBoard: %v", err)
	}

	var out dumpOutput
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	expected := []struct {
		col   int
		title string
		id    string
	}{
		{0, "Backlog Card", "bl-d1"},
		{1, "Todo Card", "bl-d2"},
		{2, "Doing Card", "bl-d3"},
		{3, "Review Card", "bl-d4"},
		{4, "Done Card", "bl-d5"},
	}

	for _, tc := range expected {
		col := out.Columns[tc.col]
		if len(col.Cards) != 1 {
			t.Errorf("column %d has %d cards, want 1", tc.col, len(col.Cards))
			continue
		}
		if col.Cards[0].ID != tc.id {
			t.Errorf("column %d card ID = %q, want %q", tc.col, col.Cards[0].ID, tc.id)
		}
		if col.Cards[0].Title != tc.title {
			t.Errorf("column %d card title = %q, want %q", tc.col, col.Cards[0].Title, tc.title)
		}
	}
}

func TestDumpBoard_ViewContainsColumnTitles(t *testing.T) {
	store := newTestStore(t)

	// Populate every column with one card so none collapse — collapsed columns
	// render abbreviated titles, not the full title the test expects to find.
	for _, tc := range []struct {
		id     string
		status beadslite.Status
	}{
		{"bl-ct1", beadslite.StatusBacklog},
		{"bl-ct2", beadslite.StatusTodo},
		{"bl-ct3", beadslite.StatusDoing},
		{"bl-ct4", beadslite.StatusReview},
		{"bl-ct5", beadslite.StatusDone},
	} {
		if err := store.CreateIssue(makeIssue(tc.id, "card", tc.status)); err != nil {
			t.Fatalf("CreateIssue(%s): %v", tc.id, err)
		}
	}

	var buf bytes.Buffer
	if err := dumpBoard(store, 120, 40, &buf); err != nil {
		t.Fatalf("dumpBoard: %v", err)
	}

	var out dumpOutput
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for _, title := range columnTitles {
		if !bytes.Contains([]byte(out.View), []byte(title)) {
			t.Errorf("view missing column title %q", title)
		}
	}
}

func TestDumpBoard_ViewContainsCardTitles(t *testing.T) {
	store := newTestStore(t)
	store.CreateIssue(makeIssue("bl-v1", "Visible Card", beadslite.StatusTodo))

	var buf bytes.Buffer
	if err := dumpBoard(store, 120, 40, &buf); err != nil {
		t.Fatalf("dumpBoard: %v", err)
	}

	var out dumpOutput
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !bytes.Contains([]byte(out.View), []byte("Visible Card")) {
		t.Error("view should contain card title 'Visible Card'")
	}
}

func TestDumpBoard_NarrowWidthPans(t *testing.T) {
	store := newTestStore(t)

	// At 60 chars wide, minColumnWidth=24 means ~2 visible columns.
	// Default focus is column 0, so panOffset should be 0.
	var buf bytes.Buffer
	if err := dumpBoard(store, 60, 40, &buf); err != nil {
		t.Fatalf("dumpBoard: %v", err)
	}

	var out dumpOutput
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if out.PanOffset != 0 {
		t.Errorf("pan_offset = %d, want 0 (focused on first column)", out.PanOffset)
	}

	// View should contain Backlog (visible) but not Done (panned out)
	if !bytes.Contains([]byte(out.View), []byte("Backlog")) {
		t.Error("narrow view should show Backlog column")
	}
}

func TestDumpBoard_ColumnTitlesMatch(t *testing.T) {
	store := newTestStore(t)

	var buf bytes.Buffer
	if err := dumpBoard(store, 120, 40, &buf); err != nil {
		t.Fatalf("dumpBoard: %v", err)
	}

	var out dumpOutput
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	wantTitles := []string{"Backlog", "To Do", "Doing", "Review", "Done"}
	for i, col := range out.Columns {
		if col.Title != wantTitles[i] {
			t.Errorf("column %d title = %q, want %q", i, col.Title, wantTitles[i])
		}
	}
}

func TestDumpBoard_CardFieldsPreserved(t *testing.T) {
	store := newTestStore(t)

	issue := makeIssue("bl-fp", "Field Test", beadslite.StatusDoing)
	issue.Priority = 0
	issue.Type = beadslite.IssueTypeBug
	store.CreateIssue(issue)

	var buf bytes.Buffer
	if err := dumpBoard(store, 120, 40, &buf); err != nil {
		t.Fatalf("dumpBoard: %v", err)
	}

	var out dumpOutput
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Card should be in Doing column (index 2)
	doingCol := out.Columns[colDoing]
	if len(doingCol.Cards) != 1 {
		t.Fatalf("doing column has %d cards, want 1", len(doingCol.Cards))
	}

	c := doingCol.Cards[0]
	if c.ID != "bl-fp" {
		t.Errorf("id = %q, want %q", c.ID, "bl-fp")
	}
	if c.Priority != 0 {
		t.Errorf("priority = %d, want 0", c.Priority)
	}
	if c.Type != "bug" {
		t.Errorf("type = %q, want %q", c.Type, "bug")
	}
	if c.Status != "doing" {
		t.Errorf("status = %q, want %q", c.Status, "doing")
	}
}

func TestDumpBoard_ValidJSON(t *testing.T) {
	store := newTestStore(t)
	store.CreateIssue(makeIssue("bl-j1", "JSON Test", beadslite.StatusTodo))

	var buf bytes.Buffer
	if err := dumpBoard(store, 120, 40, &buf); err != nil {
		t.Fatalf("dumpBoard: %v", err)
	}

	// Verify the output is exactly one line of valid JSON
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if len(lines) != 1 {
		t.Errorf("output has %d lines, want 1", len(lines))
	}

	if !json.Valid(lines[0]) {
		t.Error("output is not valid JSON")
	}
}
