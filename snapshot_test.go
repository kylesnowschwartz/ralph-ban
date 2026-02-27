package main

import (
	"bytes"
	"encoding/json"
	"testing"

	beadslite "github.com/kylesnowschwartz/beads-lite"
)

func TestWriteSnapshot_EmptyStore(t *testing.T) {
	store := newTestStore(t)
	var buf bytes.Buffer

	if err := writeSnapshot(store, &buf); err != nil {
		t.Fatalf("writeSnapshot: %v", err)
	}

	var out snapshotOutput
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(out.Columns) != int(numColumns) {
		t.Fatalf("columns = %d, want %d", len(out.Columns), numColumns)
	}
	if out.Total != 0 {
		t.Errorf("total = %d, want 0", out.Total)
	}
	for i, col := range out.Columns {
		if len(col.Cards) != 0 {
			t.Errorf("column %d (%s) has %d cards, want 0", i, col.Title, len(col.Cards))
		}
		if col.WIP != 0 {
			t.Errorf("column %d (%s) wip = %d, want 0", i, col.Title, col.WIP)
		}
	}
}

func TestWriteSnapshot_ColumnTitlesCorrect(t *testing.T) {
	store := newTestStore(t)
	var buf bytes.Buffer

	if err := writeSnapshot(store, &buf); err != nil {
		t.Fatalf("writeSnapshot: %v", err)
	}

	var out snapshotOutput
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

func TestWriteSnapshot_CardsInCorrectColumns(t *testing.T) {
	store := newTestStore(t)

	issues := []*beadslite.Issue{
		makeIssue("bl-s1", "Backlog Card", beadslite.StatusBacklog),
		makeIssue("bl-s2", "Todo Card", beadslite.StatusTodo),
		makeIssue("bl-s3", "Doing Card", beadslite.StatusDoing),
		makeIssue("bl-s4", "Review Card", beadslite.StatusReview),
		makeIssue("bl-s5", "Done Card", beadslite.StatusDone),
	}
	for _, issue := range issues {
		if err := store.CreateIssue(issue); err != nil {
			t.Fatalf("CreateIssue(%s): %v", issue.ID, err)
		}
	}

	var buf bytes.Buffer
	if err := writeSnapshot(store, &buf); err != nil {
		t.Fatalf("writeSnapshot: %v", err)
	}

	var out snapshotOutput
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	expected := []struct {
		col   int
		id    string
		title string
	}{
		{0, "bl-s1", "Backlog Card"},
		{1, "bl-s2", "Todo Card"},
		{2, "bl-s3", "Doing Card"},
		{3, "bl-s4", "Review Card"},
		{4, "bl-s5", "Done Card"},
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

func TestWriteSnapshot_WIPCounts(t *testing.T) {
	store := newTestStore(t)

	// 3 cards in todo, 2 in doing
	for _, tc := range []struct {
		id     string
		status beadslite.Status
	}{
		{"bl-w1", beadslite.StatusTodo},
		{"bl-w2", beadslite.StatusTodo},
		{"bl-w3", beadslite.StatusTodo},
		{"bl-w4", beadslite.StatusDoing},
		{"bl-w5", beadslite.StatusDoing},
	} {
		if err := store.CreateIssue(makeIssue(tc.id, "card "+tc.id, tc.status)); err != nil {
			t.Fatalf("CreateIssue(%s): %v", tc.id, err)
		}
	}

	var buf bytes.Buffer
	if err := writeSnapshot(store, &buf); err != nil {
		t.Fatalf("writeSnapshot: %v", err)
	}

	var out snapshotOutput
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if out.Columns[colTodo].WIP != 3 {
		t.Errorf("todo wip = %d, want 3", out.Columns[colTodo].WIP)
	}
	if out.Columns[colDoing].WIP != 2 {
		t.Errorf("doing wip = %d, want 2", out.Columns[colDoing].WIP)
	}
	if out.Total != 5 {
		t.Errorf("total = %d, want 5", out.Total)
	}
}

func TestWriteSnapshot_CardFieldsPreserved(t *testing.T) {
	store := newTestStore(t)

	issue := makeIssue("bl-sf", "Field Test", beadslite.StatusDoing)
	issue.Priority = 0
	issue.Type = beadslite.IssueTypeBug
	issue.AssignedTo = "worker-abc"
	if err := store.CreateIssue(issue); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	var buf bytes.Buffer
	if err := writeSnapshot(store, &buf); err != nil {
		t.Fatalf("writeSnapshot: %v", err)
	}

	var out snapshotOutput
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	doingCol := out.Columns[colDoing]
	if len(doingCol.Cards) != 1 {
		t.Fatalf("doing column has %d cards, want 1", len(doingCol.Cards))
	}

	c := doingCol.Cards[0]
	if c.ID != "bl-sf" {
		t.Errorf("id = %q, want %q", c.ID, "bl-sf")
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
	if c.Assignee != "worker-abc" {
		t.Errorf("assignee = %q, want %q", c.Assignee, "worker-abc")
	}
}

func TestWriteSnapshot_AssigneeOmittedWhenEmpty(t *testing.T) {
	store := newTestStore(t)

	issue := makeIssue("bl-noassign", "Unassigned", beadslite.StatusTodo)
	// AssignedTo is "" by default from makeIssue
	if err := store.CreateIssue(issue); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	var buf bytes.Buffer
	if err := writeSnapshot(store, &buf); err != nil {
		t.Fatalf("writeSnapshot: %v", err)
	}

	// Verify "assignee" key is absent for unassigned cards (omitempty)
	raw := buf.String()
	if bytes.Contains([]byte(raw), []byte(`"assignee"`)) {
		t.Error("assignee field should be omitted when empty")
	}
}

func TestWriteSnapshot_ValidJSON(t *testing.T) {
	store := newTestStore(t)
	store.CreateIssue(makeIssue("bl-vj", "JSON Test", beadslite.StatusTodo))

	var buf bytes.Buffer
	if err := writeSnapshot(store, &buf); err != nil {
		t.Fatalf("writeSnapshot: %v", err)
	}

	if !json.Valid(buf.Bytes()) {
		t.Error("output is not valid JSON")
	}
}

func TestWriteSnapshotASCII_ContainsColumnTitles(t *testing.T) {
	store := newTestStore(t)

	// Populate every column with one card so none collapse — collapsed columns
	// render abbreviated titles, not the full title the test expects to find.
	for _, tc := range []struct {
		id     string
		status beadslite.Status
	}{
		{"bl-at1", beadslite.StatusBacklog},
		{"bl-at2", beadslite.StatusTodo},
		{"bl-at3", beadslite.StatusDoing},
		{"bl-at4", beadslite.StatusReview},
		{"bl-at5", beadslite.StatusDone},
	} {
		if err := store.CreateIssue(makeIssue(tc.id, "card", tc.status)); err != nil {
			t.Fatalf("CreateIssue(%s): %v", tc.id, err)
		}
	}

	var buf bytes.Buffer

	if err := writeSnapshotASCII(store, 120, 40, &buf); err != nil {
		t.Fatalf("writeSnapshotASCII: %v", err)
	}

	output := buf.String()
	if output == "" {
		t.Fatal("ascii output should not be empty")
	}

	for _, title := range columnTitles {
		if !bytes.Contains([]byte(output), []byte(title)) {
			t.Errorf("ascii output missing column title %q", title)
		}
	}
}

func TestWriteSnapshotASCII_ContainsCardTitle(t *testing.T) {
	store := newTestStore(t)
	store.CreateIssue(makeIssue("bl-asc", "ASCII Visible Card", beadslite.StatusTodo))

	var buf bytes.Buffer
	if err := writeSnapshotASCII(store, 120, 40, &buf); err != nil {
		t.Fatalf("writeSnapshotASCII: %v", err)
	}

	if !bytes.Contains(buf.Bytes(), []byte("ASCII Visible Card")) {
		t.Error("ascii output should contain the card title")
	}
}
