package main

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	beadslite "github.com/kylesnowschwartz/beads-lite"
)

// newTestBoard creates a board with an in-memory store and reasonable
// terminal dimensions so columns are usable in tests.
func newTestBoard(t *testing.T) *board {
	t.Helper()
	store := newTestStore(t)
	b := newBoard(store)
	b.termWidth = 120
	b.termHeight = 40
	b.loaded = true
	// Clear WIP config so tests don't depend on whatever .ralph-ban/config.json
	// happens to exist on the developer's machine.
	b.wip = boardConfig{}
	for i := columnIndex(0); i < numColumns; i++ {
		b.cols[i].wipLimit = 0
	}
	b.resizeColumns()
	return b
}

// --- newBoard column focus initialization ---

func TestNewBoardFocusesOnlyFirstColumn(t *testing.T) {
	store := newTestStore(t)
	b := newBoard(store)

	if !b.cols[0].Focused() {
		t.Error("column 0 should be focused after newBoard")
	}
	for i := columnIndex(1); i < numColumns; i++ {
		if b.cols[i].Focused() {
			t.Errorf("column %d should be blurred after newBoard", i)
		}
	}
}

// --- handlePriority boundary clamping ---

func TestHandlePriority_ClampAtP0(t *testing.T) {
	b := newTestBoard(t)

	issue := makeIssue("bl-p0", "Already P0", beadslite.StatusTodo)
	issue.Priority = 0

	b.focused = colTodo
	b.cols[colTodo].Focus()
	b.cols[colTodo].SetItems([]list.Item{card{issue: issue}})

	cmd := b.handlePriority(priorityMsg{
		card:  card{issue: issue},
		delta: -1, // try to go above P0
	})

	if cmd != nil {
		t.Error("handlePriority should return nil at P0 boundary")
	}
	if issue.Priority != 0 {
		t.Errorf("priority = %d, want 0 (should not change)", issue.Priority)
	}
}

func TestHandlePriority_ClampAtP4(t *testing.T) {
	b := newTestBoard(t)

	issue := makeIssue("bl-p4", "Already P4", beadslite.StatusTodo)
	issue.Priority = 4

	b.focused = colTodo
	b.cols[colTodo].Focus()
	b.cols[colTodo].SetItems([]list.Item{card{issue: issue}})

	cmd := b.handlePriority(priorityMsg{
		card:  card{issue: issue},
		delta: 1, // try to go below P4
	})

	if cmd != nil {
		t.Error("handlePriority should return nil at P4 boundary")
	}
	if issue.Priority != 4 {
		t.Errorf("priority = %d, want 4 (should not change)", issue.Priority)
	}
}

func TestHandlePriority_ValidAdjustment(t *testing.T) {
	b := newTestBoard(t)

	issue := makeIssue("bl-p2", "Middle Priority", beadslite.StatusTodo)
	issue.Priority = 2

	b.focused = colTodo
	b.cols[colTodo].Focus()
	b.cols[colTodo].SetItems([]list.Item{card{issue: issue}})

	cmd := b.handlePriority(priorityMsg{
		card:  card{issue: issue},
		delta: -1,
	})

	if cmd == nil {
		t.Fatal("handlePriority should return a persist command for valid adjustment")
	}
	if issue.Priority != 1 {
		t.Errorf("priority = %d, want 1", issue.Priority)
	}
}

func TestHandlePriority_ResortsColumn(t *testing.T) {
	b := newTestBoard(t)

	cardA := makeIssue("bl-a", "Was High", beadslite.StatusTodo)
	cardA.Priority = 0
	cardB := makeIssue("bl-b", "Was Low", beadslite.StatusTodo)
	cardB.Priority = 1

	b.focused = colTodo
	b.cols[colTodo].Focus()
	b.cols[colTodo].SetItems([]list.Item{
		card{issue: cardA},
		card{issue: cardB},
	})

	// Lower cardA from P0 to P2 — should sort after cardB (P1).
	b.handlePriority(priorityMsg{
		card:  card{issue: cardA},
		delta: 2,
	})

	items := b.cols[colTodo].list.Items()
	if len(items) != 2 {
		t.Fatalf("todo has %d items, want 2", len(items))
	}
	first := items[0].(card)
	second := items[1].(card)

	if first.issue.ID != "bl-b" {
		t.Errorf("first card = %q, want bl-b (P1 should sort before P2)", first.issue.ID)
	}
	if second.issue.ID != "bl-a" {
		t.Errorf("second card = %q, want bl-a (P2 should sort after P1)", second.issue.ID)
	}
}

// --- undoLast ---

func TestUndoLastMove_ReversesMove(t *testing.T) {
	b := newTestBoard(t)

	issue := makeIssue("bl-undo", "Undo Me", beadslite.StatusDoing)

	// Simulate: card was moved from Todo to Doing.
	// It currently sits in the Doing column.
	b.cols[colDoing].SetItems([]list.Item{card{issue: issue}})
	b.cols[b.focused].Blur()
	b.focused = colDoing
	b.cols[colDoing].Focus()
	b.undo.push(undoEntry{
		kind: undoMove,
		move: &moveMsg{
			card:   card{issue: issue},
			source: colTodo,
			target: colDoing,
		},
	})

	cmd := b.undoLast()

	if cmd == nil {
		t.Fatal("undoLast should return a persist command")
	}

	// Card should be back in todo
	todoItems := b.cols[colTodo].list.Items()
	if len(todoItems) != 1 {
		t.Errorf("todo has %d items after undo, want 1", len(todoItems))
	}

	// Card should be removed from doing
	doingItems := b.cols[colDoing].list.Items()
	if len(doingItems) != 0 {
		t.Errorf("doing has %d items after undo, want 0", len(doingItems))
	}

	// Focus should follow back to source column
	if b.focused != colTodo {
		t.Errorf("focused = %d, want %d (colTodo)", b.focused, colTodo)
	}

	// Stack should be empty after one undo
	if len(b.undo) != 0 {
		t.Errorf("undo stack len = %d after undo, want 0", len(b.undo))
	}
}

func TestUndoLastMove_NilWhenNoHistory(t *testing.T) {
	b := newTestBoard(t)

	cmd := b.undoLast()

	if cmd != nil {
		t.Error("undoLast should return nil when no move to undo")
	}
}

func TestUndoLastMove_MultiStep(t *testing.T) {
	b := newTestBoard(t)

	issueA := makeIssue("bl-a", "Card A", beadslite.StatusDoing)
	issueB := makeIssue("bl-b", "Card B", beadslite.StatusReview)

	b.cols[colDoing].SetItems([]list.Item{card{issue: issueA}})
	b.cols[colReview].SetItems([]list.Item{card{issue: issueB}})

	// Push two move entries: A went todo→doing, B went doing→review.
	b.undo.push(undoEntry{
		kind: undoMove,
		move: &moveMsg{card: card{issue: issueA}, source: colTodo, target: colDoing},
	})
	b.undo.push(undoEntry{
		kind: undoMove,
		move: &moveMsg{card: card{issue: issueB}, source: colDoing, target: colReview},
	})

	b.cols[b.focused].Blur()
	b.focused = colReview
	b.cols[colReview].Focus()

	// First undo: reverses B back to doing
	cmd := b.undoLast()
	if cmd == nil {
		t.Fatal("first undo should return a command")
	}
	if len(b.cols[colReview].list.Items()) != 0 {
		t.Errorf("review should be empty after first undo, got %d items", len(b.cols[colReview].list.Items()))
	}
	if len(b.cols[colDoing].list.Items()) != 2 {
		t.Errorf("doing should have 2 items after first undo, got %d", len(b.cols[colDoing].list.Items()))
	}

	// Second undo: reverses A back to todo
	cmd = b.undoLast()
	if cmd == nil {
		t.Fatal("second undo should return a command")
	}
	if len(b.cols[colTodo].list.Items()) != 1 {
		t.Errorf("todo should have 1 item after second undo, got %d", len(b.cols[colTodo].list.Items()))
	}

	// Stack should be empty
	if len(b.undo) != 0 {
		t.Errorf("undo stack should be empty after all undos, got %d", len(b.undo))
	}
}

func TestUndoLastMove_UndoPriorityChange(t *testing.T) {
	b := newTestBoard(t)

	issue := makeIssue("bl-pri", "Priority Card", beadslite.StatusTodo)
	issue.Priority = 1 // already raised from 2

	b.focused = colTodo
	b.cols[colTodo].Focus()
	b.cols[colTodo].SetItems([]list.Item{card{issue: issue}})

	// Record that priority was 2 before the user raised it to 1.
	b.undo.push(undoEntry{
		kind:           undoPriority,
		priorityCardID: issue.ID,
		priorityCol:    colTodo,
		oldPriority:    2,
	})

	cmd := b.undoLast()
	if cmd == nil {
		t.Fatal("undo priority change should return a persist command")
	}

	// The card's priority should be restored to 2.
	items := b.cols[colTodo].list.Items()
	if len(items) != 1 {
		t.Fatalf("todo has %d items, want 1", len(items))
	}
	restored := items[0].(card)
	if restored.issue.Priority != 2 {
		t.Errorf("priority after undo = %d, want 2", restored.issue.Priority)
	}
}

func TestUndoLastMove_UndoEdit(t *testing.T) {
	b := newTestBoard(t)

	originalIssue := makeIssue("bl-edit", "Original Title", beadslite.StatusTodo)
	editedIssue := makeIssue("bl-edit", "Edited Title", beadslite.StatusTodo)

	b.focused = colTodo
	b.cols[colTodo].Focus()
	// The column currently shows the edited version.
	b.cols[colTodo].SetItems([]list.Item{card{issue: editedIssue}})

	// Record the original state before the edit.
	snapshot := *originalIssue
	b.undo.push(undoEntry{
		kind:  undoEdit,
		issue: &snapshot,
	})

	cmd := b.undoLast()
	if cmd == nil {
		t.Fatal("undo edit should return a persist command")
	}

	// The column should now show the original title.
	items := b.cols[colTodo].list.Items()
	if len(items) != 1 {
		t.Fatalf("todo has %d items, want 1", len(items))
	}
	restored := items[0].(card)
	if restored.issue.Title != "Original Title" {
		t.Errorf("title after undo = %q, want %q", restored.issue.Title, "Original Title")
	}
}

func TestUndoLastMove_UndoDelete(t *testing.T) {
	b := newTestBoard(t)

	deletedIssue := makeIssue("bl-del", "Deleted Card", beadslite.StatusTodo)

	// Column is empty (card was deleted).
	b.cols[colTodo].SetItems([]list.Item{})

	// Record the deleted card.
	snapshot := *deletedIssue
	b.undo.push(undoEntry{
		kind:  undoDelete,
		issue: &snapshot,
	})

	cmd := b.undoLast()
	if cmd == nil {
		t.Fatal("undo delete should return a persist command")
	}

	// The card should be restored to the todo column.
	items := b.cols[colTodo].list.Items()
	if len(items) != 1 {
		t.Fatalf("todo has %d items after undo delete, want 1", len(items))
	}
	restored := items[0].(card)
	if restored.issue.ID != "bl-del" {
		t.Errorf("restored card ID = %q, want bl-del", restored.issue.ID)
	}
}

func TestUndoStack_PreservedOnNoOpRefresh(t *testing.T) {
	b := newTestBoard(t)

	// Put something on the undo stack.
	b.undo.push(undoEntry{kind: undoMove})
	b.undo.push(undoEntry{kind: undoPriority})

	// Simulate a no-op refresh (same fingerprint as initial state).
	b.applyRefresh(refreshMsg{issues: nil, blockedIDs: nil, fingerprint: b.lastFingerprint})

	if len(b.undo) != 2 {
		t.Errorf("undo stack len = %d after no-op refresh, want 2 (preserved)", len(b.undo))
	}
}

func TestUndoStack_ClearedOnExternalChange(t *testing.T) {
	b := newTestBoard(t)

	// Put something on the undo stack.
	b.undo.push(undoEntry{kind: undoMove})

	// Simulate a refresh with a different fingerprint (external change).
	b.applyRefresh(refreshMsg{issues: nil, blockedIDs: nil, fingerprint: 99999})

	if len(b.undo) != 0 {
		t.Errorf("undo stack len = %d after external change, want 0 (cleared)", len(b.undo))
	}
}

func TestUndoStack_PreservedAfterLocalChange(t *testing.T) {
	b := newTestBoard(t)

	// Simulate a local move (increments pendingLocalChanges via applyColumnMove).
	issue := makeIssue("bl-local", "Local Move", beadslite.StatusBacklog)
	b.cols[colBacklog].SetItems([]list.Item{card{issue: issue}})
	b.cols[b.focused].Blur()
	b.focused = colBacklog
	b.cols[colBacklog].Focus()
	b.handleMove(moveMsg{card: card{issue: issue}, source: colBacklog, target: colTodo})

	if len(b.undo) == 0 {
		t.Fatal("undo stack should have an entry after local move")
	}

	// Refresh arrives with different fingerprint (reflecting our local change).
	// pendingLocalChanges > 0, so undo should be preserved.
	b.applyRefresh(refreshMsg{issues: nil, blockedIDs: nil, fingerprint: 12345})

	if len(b.undo) == 0 {
		t.Error("undo stack was cleared after local-change refresh, should be preserved")
	}

	// Second refresh with same fingerprint (no further changes).
	// pendingLocalChanges should have drained; same fingerprint → preserved.
	b.applyRefresh(refreshMsg{issues: nil, blockedIDs: nil, fingerprint: 12345})

	if len(b.undo) == 0 {
		t.Error("undo stack was cleared on second no-op refresh, should be preserved")
	}
}

// Reproduces the specific scenario: move card from Backlog → Todo when Todo
// already has a card, then undo. The card should return to Backlog.
func TestUndoLastMove_BacklogToTodoWithExistingCard(t *testing.T) {
	b := newTestBoard(t)

	// Setup: card A in Backlog, card B already in Todo.
	cardA := makeIssue("bl-a", "Card A", beadslite.StatusBacklog)
	cardB := makeIssue("bl-b", "Card B", beadslite.StatusTodo)
	b.cols[colBacklog].SetItems([]list.Item{card{issue: cardA}})
	b.cols[colTodo].SetItems([]list.Item{card{issue: cardB}})
	b.cols[b.focused].Blur()
	b.focused = colBacklog
	b.cols[colBacklog].Focus()

	// Simulate: move card A from Backlog to Todo.
	cmd := b.handleMove(moveMsg{card: card{issue: cardA}, source: colBacklog, target: colTodo})
	if cmd == nil {
		t.Fatal("handleMove should return a persist command")
	}

	// Verify card moved: Backlog empty, Todo has 2 cards.
	if len(b.cols[colBacklog].list.Items()) != 0 {
		t.Fatalf("backlog has %d items after move, want 0", len(b.cols[colBacklog].list.Items()))
	}
	if len(b.cols[colTodo].list.Items()) != 2 {
		t.Fatalf("todo has %d items after move, want 2", len(b.cols[colTodo].list.Items()))
	}

	// Undo: card A should go back to Backlog.
	cmd = b.undoLast()
	if cmd == nil {
		t.Fatal("undoLast should return a persist command")
	}

	// Card A should be back in Backlog.
	backlogItems := b.cols[colBacklog].list.Items()
	if len(backlogItems) != 1 {
		t.Fatalf("backlog has %d items after undo, want 1", len(backlogItems))
	}
	if backlogItems[0].(card).issue.ID != "bl-a" {
		t.Errorf("backlog card ID = %q, want bl-a", backlogItems[0].(card).issue.ID)
	}

	// Card B should still be in Todo, alone.
	todoItems := b.cols[colTodo].list.Items()
	if len(todoItems) != 1 {
		t.Fatalf("todo has %d items after undo, want 1", len(todoItems))
	}
	if todoItems[0].(card).issue.ID != "bl-b" {
		t.Errorf("todo card ID = %q, want bl-b", todoItems[0].(card).issue.ID)
	}

	// Focus should follow back to Backlog.
	if b.focused != colBacklog {
		t.Errorf("focused = %d, want %d (colBacklog)", b.focused, colBacklog)
	}
}

// --- columnAtX hit-testing ---

func TestColumnAtX_MapsToCorrectColumn(t *testing.T) {
	b := newTestBoard(t) // 120 wide, 5 columns visible → 24 px each

	// Populate all columns so none collapse — empty columns shrink to
	// collapsedOuterWidth strips, which would invalidate the fixed-width assumptions.
	statuses := []beadslite.Status{
		beadslite.StatusBacklog,
		beadslite.StatusTodo,
		beadslite.StatusDoing,
		beadslite.StatusReview,
		beadslite.StatusDone,
	}
	for i, status := range statuses {
		issue := makeIssue(fmt.Sprintf("bl-x%d", i), "card", status)
		if err := b.store.CreateIssue(issue); err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
	}
	// Load items into columns and recalculate layout.
	items, err := loadIssues(b.store)
	if err != nil {
		t.Fatalf("loadIssues: %v", err)
	}
	for i := columnIndex(0); i < numColumns; i++ {
		b.cols[i].SetItems(items[i])
	}
	b.resizeColumns()

	tests := []struct {
		x    int
		want columnIndex
	}{
		{0, colBacklog},
		{23, colBacklog},
		{24, colTodo},
		{47, colTodo},
		{48, colDoing},
		{96, colDone},
		{119, colDone},
	}

	for _, tt := range tests {
		col, ok := b.columnAtX(tt.x)
		if !ok {
			t.Errorf("columnAtX(%d) returned false, want column %d", tt.x, tt.want)
			continue
		}
		if col != tt.want {
			t.Errorf("columnAtX(%d) = %d, want %d", tt.x, col, tt.want)
		}
	}
}

func TestColumnAtX_OutOfBounds(t *testing.T) {
	b := newTestBoard(t)

	if _, ok := b.columnAtX(-1); ok {
		t.Error("columnAtX(-1) should return false")
	}
	if _, ok := b.columnAtX(b.termWidth); ok {
		t.Errorf("columnAtX(%d) should return false (beyond terminal width)", b.termWidth)
	}
}

func TestColumnAtX_WithPanOffset(t *testing.T) {
	b := newTestBoard(t)
	b.termWidth = 72 // 72/24 = 3 visible columns
	b.panOffset = 2  // showing Doing, Review, Done
	b.resizeColumns()

	col, ok := b.columnAtX(0)
	if !ok {
		t.Fatal("columnAtX(0) with panOffset=2 should return true")
	}
	if col != colDoing {
		t.Errorf("columnAtX(0) with panOffset=2 = %d, want %d (colDoing)", col, colDoing)
	}
}

// --- tea.ResumeMsg ---

func TestResumeMsgTriggersRefresh(t *testing.T) {
	b := newTestBoard(t)

	// Add an issue to the store after initial load — simulates
	// external changes made while the TUI was suspended.
	issue := makeIssue("bl-resume", "Added While Suspended", beadslite.StatusTodo)
	if err := b.store.CreateIssue(issue); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	// Send ResumeMsg — board should return a loadFromStore command.
	_, cmd := b.Update(tea.ResumeMsg{})

	if cmd == nil {
		t.Fatal("ResumeMsg should return a command (loadFromStore)")
	}

	// Execute the command; it should produce a refreshMsg with the new issue.
	msg := cmd()
	rm, ok := msg.(refreshMsg)
	if !ok {
		t.Fatalf("loadFromStore returned %T, want refreshMsg", msg)
	}

	found := false
	for _, iss := range rm.issues {
		if iss.ID == "bl-resume" {
			found = true
			break
		}
	}
	if !found {
		t.Error("refreshMsg should contain the issue created while suspended")
	}
}

// --- handleMove ---

func TestHandleMove_CardMovesToTarget(t *testing.T) {
	b := newTestBoard(t)

	issue := makeIssue("bl-mv", "Move Me", beadslite.StatusTodo)
	b.cols[colTodo].SetItems([]list.Item{card{issue: issue}})

	cmd := b.handleMove(moveMsg{
		card:   card{issue: issue},
		source: colTodo,
		target: colDoing,
	})

	if cmd == nil {
		t.Fatal("handleMove should return a persist command")
	}

	// Source column should be empty.
	todoItems := b.cols[colTodo].list.Items()
	if len(todoItems) != 0 {
		t.Errorf("todo has %d items after move, want 0", len(todoItems))
	}

	// Target column should have the card.
	doingItems := b.cols[colDoing].list.Items()
	if len(doingItems) != 1 {
		t.Fatalf("doing has %d items after move, want 1", len(doingItems))
	}
	movedCard := doingItems[0].(card)
	if movedCard.issue.ID != "bl-mv" {
		t.Errorf("moved card ID = %q, want bl-mv", movedCard.issue.ID)
	}
}

func TestHandleMove_FocusFollowsCard(t *testing.T) {
	b := newTestBoard(t)

	issue := makeIssue("bl-focus", "Focus Follows", beadslite.StatusTodo)
	b.cols[colTodo].SetItems([]list.Item{card{issue: issue}})
	b.focused = colTodo
	b.cols[colTodo].Focus()

	b.handleMove(moveMsg{
		card:   card{issue: issue},
		source: colTodo,
		target: colDoing,
	})

	if b.focused != colDoing {
		t.Errorf("focused = %d after move, want %d (colDoing)", b.focused, colDoing)
	}
	if !b.cols[colDoing].Focused() {
		t.Error("colDoing should be focused after move")
	}
	if b.cols[colTodo].Focused() {
		t.Error("colTodo should be blurred after move")
	}
}

func TestHandleMove_UndoEntryPushed(t *testing.T) {
	b := newTestBoard(t)

	issue := makeIssue("bl-undo-move", "Undo Move", beadslite.StatusTodo)
	b.cols[colTodo].SetItems([]list.Item{card{issue: issue}})

	if len(b.undo) != 0 {
		t.Fatalf("undo stack should be empty before move, got %d", len(b.undo))
	}

	b.handleMove(moveMsg{
		card:   card{issue: issue},
		source: colTodo,
		target: colDoing,
	})

	if len(b.undo) != 1 {
		t.Fatalf("undo stack len = %d after move, want 1", len(b.undo))
	}
	entry := b.undo[0]
	if entry.kind != undoMove {
		t.Errorf("undo entry kind = %d, want undoMove (%d)", entry.kind, undoMove)
	}
	if entry.move.source != colTodo {
		t.Errorf("undo entry source = %d, want colTodo (%d)", entry.move.source, colTodo)
	}
	if entry.move.target != colDoing {
		t.Errorf("undo entry target = %d, want colDoing (%d)", entry.move.target, colDoing)
	}
}

func TestHandleMove_IntoDoneOpensResolutionPicker(t *testing.T) {
	b := newTestBoard(t)

	issue := makeIssue("bl-done", "Close Me", beadslite.StatusReview)
	b.cols[colReview].SetItems([]list.Item{card{issue: issue}})

	cmd := b.handleMove(moveMsg{
		card:   card{issue: issue},
		source: colReview,
		target: colDone,
	})

	// handleMove returns nil when intercepted by the resolution picker.
	if cmd != nil {
		t.Error("handleMove into Done should return nil (intercepted by resolution picker)")
	}

	// Resolution picker should be open.
	if b.view != viewResolution {
		t.Errorf("view = %d after move to Done, want viewResolution (%d)", b.view, viewResolution)
	}
	if b.resolution == nil {
		t.Error("resolution picker should be set after move to Done")
	}

	// Card should NOT have moved yet — picker hasn't confirmed.
	doneItems := b.cols[colDone].list.Items()
	if len(doneItems) != 0 {
		t.Errorf("done has %d items before resolution confirm, want 0", len(doneItems))
	}
}

func TestHandleMove_WIPLimitBlocked(t *testing.T) {
	b := newTestBoard(t)
	// Set via b.wip — handleMove reads from b.wip.wipLimit(), not b.cols[i].wipLimit.
	b.wip = boardConfig{WIPLimits: map[string]int{"doing": 1}}

	// Pre-fill doing to capacity.
	existing := makeIssue("bl-existing", "Existing", beadslite.StatusDoing)
	b.cols[colDoing].SetItems([]list.Item{card{issue: existing}})

	incoming := makeIssue("bl-incoming", "Incoming", beadslite.StatusTodo)
	b.cols[colTodo].SetItems([]list.Item{card{issue: incoming}})

	cmd := b.handleMove(moveMsg{
		card:   card{issue: incoming},
		source: colTodo,
		target: colDoing,
	})

	// Should be blocked — no command returned.
	if cmd != nil {
		t.Error("handleMove should return nil when WIP limit is reached")
	}

	// Error should be set on the board.
	if b.err == nil {
		t.Error("board error should be set when WIP limit blocks a move")
	}

	// Card should remain in todo.
	todoItems := b.cols[colTodo].list.Items()
	if len(todoItems) != 1 {
		t.Errorf("todo has %d items (card should not have moved), want 1", len(todoItems))
	}

	// Doing should still be at capacity (1), not 2.
	doingItems := b.cols[colDoing].list.Items()
	if len(doingItems) != 1 {
		t.Errorf("doing has %d items after blocked move, want 1", len(doingItems))
	}
}

// TestHandleMove_WIPLimitFilterBypass verifies that an active filter cannot
// hide existing cards and let a move slip past the WIP limit.
// When priority filter P1 is active, only P1 cards are visible — but the column
// already holds two cards total (a P1 and a P2). The WIP limit is 2, so the
// column is full. Without the fix, the visible-only count would be 1 and the
// move would be allowed incorrectly.
func TestHandleMove_WIPLimitFilterBypass(t *testing.T) {
	b := newTestBoard(t)
	b.wip = boardConfig{WIPLimits: map[string]int{"doing": 2}}

	// Two existing cards in doing — one P1 (visible under filter) and one P2 (hidden).
	p1 := makeIssue("bl-p1", "P1 Task", beadslite.StatusDoing)
	p1.Priority = 1
	p2 := makeIssue("bl-p2", "P2 Task", beadslite.StatusDoing)
	p2.Priority = 2

	// Populate allIssues (the authoritative source used when a filter is active).
	b.allIssues = []*beadslite.Issue{p1, p2}

	// Activate a P1 priority filter so only p1 is visible.
	b.filter = activeFilter{field: filterPriority, value: "P1"}
	b.cols[colDoing].SetItems([]list.Item{card{issue: p1}}) // only the matching card is shown

	// Attempt to move another card from todo into the already-full doing column.
	incoming := makeIssue("bl-incoming", "Incoming", beadslite.StatusTodo)
	b.cols[colTodo].SetItems([]list.Item{card{issue: incoming}})

	cmd := b.handleMove(moveMsg{
		card:   card{issue: incoming},
		source: colTodo,
		target: colDoing,
	})

	if cmd != nil {
		t.Error("handleMove should return nil when WIP limit is reached (filter active)")
	}
	if b.err == nil {
		t.Error("board error should be set when WIP limit blocks a move (filter active)")
	}

	// The incoming card must not have moved.
	doingItems := b.cols[colDoing].list.Items()
	if len(doingItems) != 1 {
		t.Errorf("doing has %d visible items after blocked move, want 1 (only p1)", len(doingItems))
	}
}

// --- handleMove spec-gate enforcement ---

func TestHandleMove_SpecGateBlocked(t *testing.T) {
	b := newTestBoard(t)
	// Default config has nil RequireSpecsForReview, which defaults to true.

	issue := makeIssue("bl-spec1", "Unchecked Specs", beadslite.StatusDoing)
	issue.Specifications = []beadslite.Spec{
		{Text: "write tests", Checked: true},
		{Text: "update docs", Checked: false},
	}
	b.cols[colDoing].SetItems([]list.Item{card{issue: issue}})
	b.focused = colDoing
	b.cols[colDoing].Focus()

	cmd := b.handleMove(moveMsg{
		card:   card{issue: issue},
		source: colDoing,
		target: colReview,
	})

	if cmd != nil {
		t.Error("handleMove should return nil when specs are incomplete")
	}
	if b.err == nil {
		t.Fatal("board error should be set when spec gate blocks a move")
	}
}

func TestHandleMove_SpecGateAllChecked(t *testing.T) {
	b := newTestBoard(t)

	issue := makeIssue("bl-spec2", "All Checked", beadslite.StatusDoing)
	issue.Specifications = []beadslite.Spec{
		{Text: "write tests", Checked: true},
		{Text: "update docs", Checked: true},
	}
	if err := b.store.CreateIssue(issue); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	b.cols[colDoing].SetItems([]list.Item{card{issue: issue}})
	b.focused = colDoing
	b.cols[colDoing].Focus()

	cmd := b.handleMove(moveMsg{
		card:   card{issue: issue},
		source: colDoing,
		target: colReview,
	})

	if cmd == nil {
		t.Error("handleMove should return a persist command when all specs are checked")
	}
	if b.err != nil {
		t.Errorf("no error expected on allowed move, got: %v", b.err)
	}
}

func TestHandleMove_SpecGateNoSpecs(t *testing.T) {
	b := newTestBoard(t)

	issue := makeIssue("bl-spec3", "No Specs", beadslite.StatusDoing)
	// No specifications set — should pass unconditionally.
	if err := b.store.CreateIssue(issue); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	b.cols[colDoing].SetItems([]list.Item{card{issue: issue}})
	b.focused = colDoing
	b.cols[colDoing].Focus()

	cmd := b.handleMove(moveMsg{
		card:   card{issue: issue},
		source: colDoing,
		target: colReview,
	})

	if cmd == nil {
		t.Error("handleMove should allow move when card has no specs")
	}
	if b.err != nil {
		t.Errorf("no error expected for card with no specs, got: %v", b.err)
	}
}

func TestHandleMove_SpecGateDisabled(t *testing.T) {
	b := newTestBoard(t)
	b.blConfig = beadslite.Config{RequireSpecsForReview: func() *bool { v := false; return &v }()}

	issue := makeIssue("bl-spec4", "Unchecked But Allowed", beadslite.StatusDoing)
	issue.Specifications = []beadslite.Spec{
		{Text: "write tests", Checked: false},
	}
	if err := b.store.CreateIssue(issue); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	b.cols[colDoing].SetItems([]list.Item{card{issue: issue}})
	b.focused = colDoing
	b.cols[colDoing].Focus()

	cmd := b.handleMove(moveMsg{
		card:   card{issue: issue},
		source: colDoing,
		target: colReview,
	})

	if cmd == nil {
		t.Error("handleMove should allow move when spec gate is disabled")
	}
	if b.err != nil {
		t.Errorf("no error expected when spec gate is disabled, got: %v", b.err)
	}
}

func TestHandleMove_SpecGateOnlyReview(t *testing.T) {
	b := newTestBoard(t)
	// Default config: specs required for review.

	issue := makeIssue("bl-spec5", "Unchecked Non-Review", beadslite.StatusTodo)
	issue.Specifications = []beadslite.Spec{
		{Text: "write tests", Checked: false},
	}
	if err := b.store.CreateIssue(issue); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	b.cols[colTodo].SetItems([]list.Item{card{issue: issue}})
	b.focused = colTodo
	b.cols[colTodo].Focus()

	cmd := b.handleMove(moveMsg{
		card:   card{issue: issue},
		source: colTodo,
		target: colDoing,
	})

	if cmd == nil {
		t.Error("handleMove should allow move to non-review column even with unchecked specs")
	}
	if b.err != nil {
		t.Errorf("no error expected for non-review move, got: %v", b.err)
	}
}

func TestHandleMove_ClearsErrorOnSuccess(t *testing.T) {
	b := newTestBoard(t)

	// Set a pre-existing error to verify it gets cleared.
	b.err = fmt.Errorf("prior error")

	issue := makeIssue("bl-clear", "Clear Error", beadslite.StatusTodo)
	b.cols[colTodo].SetItems([]list.Item{card{issue: issue}})

	b.handleMove(moveMsg{
		card:   card{issue: issue},
		source: colTodo,
		target: colDoing,
	})

	if b.err != nil {
		t.Errorf("board error should be cleared after successful move, got: %v", b.err)
	}
}

func TestHandleMove_CardLandsAtPrioritySortedPosition(t *testing.T) {
	b := newTestBoard(t)

	// Target column already has a P1 and P3 card.
	p1 := makeIssue("bl-p1", "P1 Card", beadslite.StatusDoing)
	p1.Priority = 1
	p3 := makeIssue("bl-p3", "P3 Card", beadslite.StatusDoing)
	p3.Priority = 3
	b.cols[colDoing].SetItems([]list.Item{
		card{issue: p1},
		card{issue: p3},
	})

	// Move a P2 card from Todo into Doing — it should land between P1 and P3.
	p2 := makeIssue("bl-p2", "P2 Card", beadslite.StatusTodo)
	p2.Priority = 2
	b.cols[colTodo].SetItems([]list.Item{card{issue: p2}})
	b.focused = colTodo
	b.cols[colTodo].Focus()

	b.handleMove(moveMsg{
		card:   card{issue: p2},
		source: colTodo,
		target: colDoing,
	})

	items := b.cols[colDoing].list.Items()
	if len(items) != 3 {
		t.Fatalf("doing has %d items after move, want 3", len(items))
	}

	// P1 should be first, P2 in the middle, P3 last.
	if items[0].(card).issue.ID != "bl-p1" {
		t.Errorf("doing[0] = %q, want bl-p1", items[0].(card).issue.ID)
	}
	if items[1].(card).issue.ID != "bl-p2" {
		t.Errorf("doing[1] = %q, want bl-p2 (moved card at priority-sorted position)", items[1].(card).issue.ID)
	}
	if items[2].(card).issue.ID != "bl-p3" {
		t.Errorf("doing[2] = %q, want bl-p3", items[2].(card).issue.ID)
	}

	// List cursor should point at the moved card (index 1), not the tail.
	if b.cols[colDoing].list.Index() != 1 {
		t.Errorf("cursor = %d after move, want 1 (moved card's sorted index)", b.cols[colDoing].list.Index())
	}
}

func TestHandleMove_HighPriorityCardLandsAtTop(t *testing.T) {
	b := newTestBoard(t)

	// Target column has a P2 card.
	p2 := makeIssue("bl-p2", "P2 Card", beadslite.StatusDoing)
	p2.Priority = 2
	b.cols[colDoing].SetItems([]list.Item{card{issue: p2}})

	// Move a P0 card in — it should be first (highest priority).
	p0 := makeIssue("bl-p0", "P0 Card", beadslite.StatusTodo)
	p0.Priority = 0
	b.cols[colTodo].SetItems([]list.Item{card{issue: p0}})
	b.focused = colTodo
	b.cols[colTodo].Focus()

	b.handleMove(moveMsg{
		card:   card{issue: p0},
		source: colTodo,
		target: colDoing,
	})

	items := b.cols[colDoing].list.Items()
	if len(items) != 2 {
		t.Fatalf("doing has %d items, want 2", len(items))
	}
	if items[0].(card).issue.ID != "bl-p0" {
		t.Errorf("doing[0] = %q, want bl-p0 (P0 sorts to top)", items[0].(card).issue.ID)
	}

	// Cursor should follow the card to index 0.
	if b.cols[colDoing].list.Index() != 0 {
		t.Errorf("cursor = %d after move, want 0", b.cols[colDoing].list.Index())
	}
}

func TestApplyUndoMove_CardLandsAtPrioritySortedPosition(t *testing.T) {
	b := newTestBoard(t)

	// The original column (Todo) already has P0 and P4 cards.
	p0 := makeIssue("bl-p0", "P0 Card", beadslite.StatusTodo)
	p0.Priority = 0
	p4 := makeIssue("bl-p4", "P4 Card", beadslite.StatusTodo)
	p4.Priority = 4
	b.cols[colTodo].SetItems([]list.Item{
		card{issue: p0},
		card{issue: p4},
	})

	// The card being undone is currently in Doing.
	p2 := makeIssue("bl-p2", "P2 Card", beadslite.StatusDoing)
	p2.Priority = 2
	b.cols[colDoing].SetItems([]list.Item{card{issue: p2}})
	b.cols[b.focused].Blur()
	b.focused = colDoing
	b.cols[colDoing].Focus()

	// Undo the move: card goes back from Doing to Todo.
	b.undo.push(undoEntry{
		kind: undoMove,
		move: &moveMsg{
			card:   card{issue: p2},
			source: colTodo,
			target: colDoing,
		},
	})

	cmd := b.undoLast()
	if cmd == nil {
		t.Fatal("undoLast should return a persist command")
	}

	// Card should be back in Todo, sorted between P0 and P4.
	todoItems := b.cols[colTodo].list.Items()
	if len(todoItems) != 3 {
		t.Fatalf("todo has %d items after undo, want 3", len(todoItems))
	}
	if todoItems[0].(card).issue.ID != "bl-p0" {
		t.Errorf("todo[0] = %q, want bl-p0", todoItems[0].(card).issue.ID)
	}
	if todoItems[1].(card).issue.ID != "bl-p2" {
		t.Errorf("todo[1] = %q, want bl-p2 (undone card at priority-sorted position)", todoItems[1].(card).issue.ID)
	}
	if todoItems[2].(card).issue.ID != "bl-p4" {
		t.Errorf("todo[2] = %q, want bl-p4", todoItems[2].(card).issue.ID)
	}

	// Cursor should follow the card to index 1.
	if b.cols[colTodo].list.Index() != 1 {
		t.Errorf("cursor = %d after undo, want 1 (undone card's sorted index)", b.cols[colTodo].list.Index())
	}
}

// --- handleSave ---

func TestHandleSave_NilIssueReturnsNil(t *testing.T) {
	b := newTestBoard(t)

	cmd := b.handleSave(saveMsg{issue: nil})

	if cmd != nil {
		t.Error("handleSave with nil issue should return nil")
	}
	// View should be reset to board.
	if b.view != viewBoard {
		t.Errorf("view = %d after nil save, want viewBoard (%d)", b.view, viewBoard)
	}
}

func TestHandleSave_EditExistingCard(t *testing.T) {
	b := newTestBoard(t)

	originalIssue := makeIssue("bl-edit", "Original Title", beadslite.StatusTodo)
	b.cols[colTodo].SetItems([]list.Item{card{issue: originalIssue}})

	// Submit an edit with a new title.
	updatedIssue := makeIssue("bl-edit", "Updated Title", beadslite.StatusTodo)
	cmd := b.handleSave(saveMsg{issue: updatedIssue})

	if cmd == nil {
		t.Fatal("handleSave for edit should return a persist command")
	}

	// Card should be updated in the column.
	items := b.cols[colTodo].list.Items()
	if len(items) != 1 {
		t.Fatalf("todo has %d items after edit, want 1", len(items))
	}
	editedCard := items[0].(card)
	if editedCard.issue.Title != "Updated Title" {
		t.Errorf("card title = %q after edit, want %q", editedCard.issue.Title, "Updated Title")
	}
}

func TestHandleSave_EditPushesUndoEntry(t *testing.T) {
	b := newTestBoard(t)

	originalIssue := makeIssue("bl-edit-undo", "Before", beadslite.StatusTodo)
	b.cols[colTodo].SetItems([]list.Item{card{issue: originalIssue}})

	updatedIssue := makeIssue("bl-edit-undo", "After", beadslite.StatusTodo)
	b.handleSave(saveMsg{issue: updatedIssue})

	if len(b.undo) != 1 {
		t.Fatalf("undo stack len = %d after edit, want 1", len(b.undo))
	}
	entry := b.undo[0]
	if entry.kind != undoEdit {
		t.Errorf("undo entry kind = %d, want undoEdit (%d)", entry.kind, undoEdit)
	}
	if entry.issue == nil {
		t.Fatal("undo entry issue should not be nil for edit")
	}
	// Snapshot should hold the old title.
	if entry.issue.Title != "Before" {
		t.Errorf("undo snapshot title = %q, want %q", entry.issue.Title, "Before")
	}
}

func TestHandleSave_CreateNewCard(t *testing.T) {
	b := newTestBoard(t)

	newIssue := makeIssue("bl-new", "Brand New", beadslite.StatusTodo)
	cmd := b.handleSave(saveMsg{issue: newIssue})

	if cmd == nil {
		t.Fatal("handleSave for create should return a persist command")
	}

	// Card should appear in the appropriate column.
	items := b.cols[colTodo].list.Items()
	if len(items) != 1 {
		t.Fatalf("todo has %d items after create, want 1", len(items))
	}
	createdCard := items[0].(card)
	if createdCard.issue.ID != "bl-new" {
		t.Errorf("created card ID = %q, want bl-new", createdCard.issue.ID)
	}
}

func TestHandleSave_CreateDoesNotPushUndo(t *testing.T) {
	b := newTestBoard(t)

	newIssue := makeIssue("bl-no-undo", "New Card", beadslite.StatusBacklog)
	b.handleSave(saveMsg{issue: newIssue})

	// Creating a new card doesn't record an undo entry — there's nothing to undo back to.
	if len(b.undo) != 0 {
		t.Errorf("undo stack len = %d after create, want 0", len(b.undo))
	}
}

func TestHandleSave_CreateAddsToCorrectColumn(t *testing.T) {
	b := newTestBoard(t)

	// A card with StatusDoing should land in the Doing column.
	doingIssue := makeIssue("bl-doing", "In Progress", beadslite.StatusDoing)
	b.handleSave(saveMsg{issue: doingIssue})

	if len(b.cols[colDoing].list.Items()) != 1 {
		t.Errorf("doing has %d items, want 1", len(b.cols[colDoing].list.Items()))
	}
	// Other columns should be empty.
	for i := columnIndex(0); i < numColumns; i++ {
		if i == colDoing {
			continue
		}
		if len(b.cols[i].list.Items()) != 0 {
			t.Errorf("column %d has %d items, want 0", i, len(b.cols[i].list.Items()))
		}
	}
}

func TestHandleSave_CreateSelectsNewCard(t *testing.T) {
	b := newTestBoard(t)

	// Pre-fill the todo column with an existing card so the cursor starts elsewhere.
	existing := makeIssue("bl-existing", "Existing Card", beadslite.StatusTodo)
	existing.Priority = 1
	b.cols[colTodo].SetItems([]list.Item{card{issue: existing}})
	b.cols[colTodo].list.Select(0)

	// Create a new card with higher priority so it sorts before the existing one.
	newIssue := makeIssue("bl-new", "New High Priority", beadslite.StatusTodo)
	newIssue.Priority = 0
	b.handleSave(saveMsg{issue: newIssue})

	// Column should now have both cards.
	items := b.cols[colTodo].list.Items()
	if len(items) != 2 {
		t.Fatalf("todo has %d items after create, want 2", len(items))
	}

	// P0 card should sort first.
	if items[0].(card).issue.ID != "bl-new" {
		t.Errorf("first card = %q after sort, want bl-new (P0)", items[0].(card).issue.ID)
	}

	// The newly created card should be selected (index 0 — sorted to top by priority).
	selected := b.cols[colTodo].list.Index()
	if selected != 0 {
		t.Errorf("selected index = %d after create, want 0 (newly created P0 card)", selected)
	}
	selectedCard := b.cols[colTodo].list.Items()[selected].(card)
	if selectedCard.issue.ID != "bl-new" {
		t.Errorf("selected card ID = %q, want bl-new", selectedCard.issue.ID)
	}
}

func TestHandleSave_CreateSelectsNewCard_AppendedAtEnd(t *testing.T) {
	b := newTestBoard(t)

	// Pre-fill with a higher-priority card.
	existing := makeIssue("bl-high", "High Priority", beadslite.StatusTodo)
	existing.Priority = 0
	b.cols[colTodo].SetItems([]list.Item{card{issue: existing}})

	// Create a lower-priority card — it sorts after the existing one.
	newIssue := makeIssue("bl-low", "Low Priority", beadslite.StatusTodo)
	newIssue.Priority = 3
	b.handleSave(saveMsg{issue: newIssue})

	items := b.cols[colTodo].list.Items()
	if len(items) != 2 {
		t.Fatalf("todo has %d items after create, want 2", len(items))
	}

	// New card should be selected even though it sorted to the end.
	selected := b.cols[colTodo].list.Index()
	selectedCard := items[selected].(card)
	if selectedCard.issue.ID != "bl-low" {
		t.Errorf("selected card ID = %q, want bl-low (newly created card)", selectedCard.issue.ID)
	}
}

func TestHandleSave_ResetsViewToBoard(t *testing.T) {
	b := newTestBoard(t)
	b.view = viewForm

	// Even with a nil issue the view should be reset.
	b.handleSave(saveMsg{issue: nil})

	if b.view != viewBoard {
		t.Errorf("view = %d after save, want viewBoard (%d)", b.view, viewBoard)
	}
}

// --- handleClose ---

func TestHandleClose_CardMovesToDoneColumn(t *testing.T) {
	b := newTestBoard(t)

	issue := makeIssue("bl-close", "Close Card", beadslite.StatusReview)
	b.cols[colReview].SetItems([]list.Item{card{issue: issue}})

	cmd := b.handleClose(closeMsg{
		card:       card{issue: issue},
		source:     colReview,
		resolution: beadslite.ResolutionDone,
	})

	if cmd == nil {
		t.Fatal("handleClose should return a persist command")
	}

	// Card should be in Done.
	doneItems := b.cols[colDone].list.Items()
	if len(doneItems) != 1 {
		t.Fatalf("done has %d items after close, want 1", len(doneItems))
	}
	closedCard := doneItems[0].(card)
	if closedCard.issue.ID != "bl-close" {
		t.Errorf("closed card ID = %q, want bl-close", closedCard.issue.ID)
	}

	// Card should be removed from Review.
	reviewItems := b.cols[colReview].list.Items()
	if len(reviewItems) != 0 {
		t.Errorf("review has %d items after close, want 0", len(reviewItems))
	}
}

func TestHandleClose_UndoEntryPushed(t *testing.T) {
	b := newTestBoard(t)

	issue := makeIssue("bl-close-undo", "Close Undo", beadslite.StatusReview)
	b.cols[colReview].SetItems([]list.Item{card{issue: issue}})

	b.handleClose(closeMsg{
		card:       card{issue: issue},
		source:     colReview,
		resolution: beadslite.ResolutionDone,
	})

	if len(b.undo) != 1 {
		t.Fatalf("undo stack len = %d after close, want 1", len(b.undo))
	}
	entry := b.undo[0]
	if entry.kind != undoMove {
		t.Errorf("undo entry kind = %d, want undoMove (%d)", entry.kind, undoMove)
	}
	if entry.move.target != colDone {
		t.Errorf("undo entry target = %d, want colDone (%d)", entry.move.target, colDone)
	}
}

func TestHandleClose_WIPLimitOnDoneBlocks(t *testing.T) {
	b := newTestBoard(t)
	// Set via b.wip — handleClose reads from b.wip.wipLimit(), not b.cols[i].wipLimit.
	b.wip = boardConfig{WIPLimits: map[string]int{"done": 1}}

	// Fill Done to capacity.
	existing := makeIssue("bl-done-existing", "Already Done", beadslite.StatusDone)
	b.cols[colDone].SetItems([]list.Item{card{issue: existing}})

	incoming := makeIssue("bl-close-wip", "Close WIP", beadslite.StatusReview)
	b.cols[colReview].SetItems([]list.Item{card{issue: incoming}})

	cmd := b.handleClose(closeMsg{
		card:       card{issue: incoming},
		source:     colReview,
		resolution: beadslite.ResolutionDone,
	})

	// Move should be blocked.
	if cmd != nil {
		t.Error("handleClose should return nil when Done WIP limit is reached")
	}
	if b.err == nil {
		t.Error("board error should be set when Done WIP limit blocks close")
	}

	// Done should still have 1 (the existing card).
	if len(b.cols[colDone].list.Items()) != 1 {
		t.Errorf("done has %d items after blocked close, want 1", len(b.cols[colDone].list.Items()))
	}
}

// --- handleDelete ---

func TestHandleDelete_ReturnsCommand(t *testing.T) {
	b := newTestBoard(t)

	issue := makeIssue("bl-del", "Delete Me", beadslite.StatusTodo)
	b.cols[colTodo].SetItems([]list.Item{card{issue: issue}})

	cmd := b.handleDelete(deleteMsg{id: "bl-del"})

	if cmd == nil {
		t.Fatal("handleDelete should return a persist command")
	}
}

func TestHandleDelete_PushesUndoEntry(t *testing.T) {
	b := newTestBoard(t)

	issue := makeIssue("bl-del-undo", "Delete Undo", beadslite.StatusTodo)
	b.cols[colTodo].SetItems([]list.Item{card{issue: issue}})

	if len(b.undo) != 0 {
		t.Fatalf("undo stack should be empty before delete, got %d", len(b.undo))
	}

	b.handleDelete(deleteMsg{id: "bl-del-undo"})

	if len(b.undo) != 1 {
		t.Fatalf("undo stack len = %d after delete, want 1", len(b.undo))
	}
	entry := b.undo[0]
	if entry.kind != undoDelete {
		t.Errorf("undo entry kind = %d, want undoDelete (%d)", entry.kind, undoDelete)
	}
	if entry.issue == nil {
		t.Fatal("undo entry issue should not be nil for delete")
	}
	if entry.issue.ID != "bl-del-undo" {
		t.Errorf("undo snapshot ID = %q, want bl-del-undo", entry.issue.ID)
	}
}

func TestHandleDelete_SnapshotIsIndependentCopy(t *testing.T) {
	b := newTestBoard(t)

	issue := makeIssue("bl-del-copy", "Original Title", beadslite.StatusTodo)
	b.cols[colTodo].SetItems([]list.Item{card{issue: issue}})

	b.handleDelete(deleteMsg{id: "bl-del-copy"})

	// Mutating the original issue pointer should not corrupt the undo snapshot.
	issue.Title = "Mutated After Delete"

	if b.undo[0].issue.Title != "Original Title" {
		t.Errorf("undo snapshot title = %q after mutation, want %q (snapshot should be independent)",
			b.undo[0].issue.Title, "Original Title")
	}
}

func TestHandleDelete_CardNotInBoardSkipsUndo(t *testing.T) {
	b := newTestBoard(t)
	// All columns are empty — the card being deleted doesn't exist in any column.
	// handleDelete should still return a command and not panic.
	cmd := b.handleDelete(deleteMsg{id: "bl-ghost"})

	// A command is returned (persistDelete is called regardless).
	if cmd == nil {
		t.Fatal("handleDelete should return a persist command even if card not found in columns")
	}

	// No undo entry — card wasn't found to snapshot.
	if len(b.undo) != 0 {
		t.Errorf("undo stack len = %d when card not found, want 0", len(b.undo))
	}
}

// --- applyRefresh ---

func TestApplyRefresh_ColumnItemsReplaced(t *testing.T) {
	b := newTestBoard(t)

	// Pre-fill some columns with stale items.
	stale := makeIssue("bl-stale", "Stale", beadslite.StatusTodo)
	b.cols[colTodo].SetItems([]list.Item{card{issue: stale}})

	// Refresh arrives with fresh data.
	fresh1 := makeIssue("bl-fresh1", "Fresh One", beadslite.StatusTodo)
	fresh2 := makeIssue("bl-fresh2", "Fresh Two", beadslite.StatusDoing)

	b.applyRefresh(refreshMsg{
		issues:     []*beadslite.Issue{fresh1, fresh2},
		blockedIDs: nil,
	})

	todoItems := b.cols[colTodo].list.Items()
	if len(todoItems) != 1 {
		t.Fatalf("todo has %d items after refresh, want 1", len(todoItems))
	}
	if todoItems[0].(card).issue.ID != "bl-fresh1" {
		t.Errorf("todo[0] ID = %q after refresh, want bl-fresh1", todoItems[0].(card).issue.ID)
	}

	doingItems := b.cols[colDoing].list.Items()
	if len(doingItems) != 1 {
		t.Fatalf("doing has %d items after refresh, want 1", len(doingItems))
	}
	if doingItems[0].(card).issue.ID != "bl-fresh2" {
		t.Errorf("doing[0] ID = %q after refresh, want bl-fresh2", doingItems[0].(card).issue.ID)
	}
}

func TestApplyRefresh_BlockedCardsPropagated(t *testing.T) {
	b := newTestBoard(t)

	issueA := makeIssue("bl-blocked", "Blocked Card", beadslite.StatusTodo)
	issueB := makeIssue("bl-free", "Free Card", beadslite.StatusTodo)

	b.applyRefresh(refreshMsg{
		issues:     []*beadslite.Issue{issueA, issueB},
		blockedIDs: map[string]bool{"bl-blocked": true},
	})

	items := b.cols[colTodo].list.Items()
	if len(items) != 2 {
		t.Fatalf("todo has %d items, want 2", len(items))
	}

	byID := make(map[string]card)
	for _, item := range items {
		c := item.(card)
		byID[c.issue.ID] = c
	}

	if !byID["bl-blocked"].blocked {
		t.Error("bl-blocked should have blocked=true after refresh")
	}
	if byID["bl-free"].blocked {
		t.Error("bl-free should have blocked=false after refresh")
	}
}

func TestApplyRefresh_UndoStackClearedOnExternalChange(t *testing.T) {
	b := newTestBoard(t)

	// Seed the undo stack with entries that will be stale after refresh.
	b.undo.push(undoEntry{kind: undoMove})
	b.undo.push(undoEntry{kind: undoEdit})

	if len(b.undo) != 2 {
		t.Fatalf("undo stack len = %d before refresh, want 2", len(b.undo))
	}

	// Refresh with a different fingerprint (external change).
	b.applyRefresh(refreshMsg{issues: nil, blockedIDs: nil, fingerprint: 99999})

	if len(b.undo) != 0 {
		t.Errorf("undo stack len = %d after external change refresh, want 0", len(b.undo))
	}
}

func TestApplyRefresh_EmptyIssuesEmptiesColumns(t *testing.T) {
	b := newTestBoard(t)

	// Pre-fill columns.
	for i := columnIndex(0); i < numColumns; i++ {
		issue := makeIssue(fmt.Sprintf("bl-%d", i), "Card", beadslite.StatusTodo)
		b.cols[colTodo].SetItems([]list.Item{card{issue: issue}})
	}

	// Refresh with no issues — all columns should become empty.
	b.applyRefresh(refreshMsg{issues: nil, blockedIDs: nil})

	for i := columnIndex(0); i < numColumns; i++ {
		if len(b.cols[i].list.Items()) != 0 {
			t.Errorf("column %d has %d items after empty refresh, want 0", i, len(b.cols[i].list.Items()))
		}
	}
}

// --- formatDeps ---

func TestFormatDeps_EmptySlice(t *testing.T) {
	result := formatDeps(nil)
	if result != "" {
		t.Errorf("formatDeps(nil) = %q, want empty string", result)
	}
}

func TestFormatDeps_SingleEntry(t *testing.T) {
	deps := []depEntry{{id: "bl-abc", title: "Some Card"}}
	result := formatDeps(deps)
	if result != "  bl-abc  Some Card" {
		t.Errorf("formatDeps single = %q, want %q", result, "  bl-abc  Some Card")
	}
}

func TestFormatDeps_MultipleEntries(t *testing.T) {
	deps := []depEntry{
		{id: "bl-one", title: "First"},
		{id: "bl-two", title: "Second"},
	}
	result := formatDeps(deps)
	want := "  bl-one  First\n  bl-two  Second"
	if result != want {
		t.Errorf("formatDeps multiple = %q, want %q", result, want)
	}
}

// --- zoom e-to-edit transition ---

func TestZoomEditKey_TransitionsToEditForm(t *testing.T) {
	b := newTestBoard(t)

	issue := makeIssue("bl-zoom-e", "Zoom Edit", beadslite.StatusTodo)
	b.cols[b.focused].Blur()
	b.focused = colTodo
	b.cols[colTodo].Focus()
	b.cols[colTodo].SetItems([]list.Item{card{issue: issue}})

	// Simulate opening the zoom overlay.
	b.openZoom()
	if b.view != viewZoom {
		t.Fatalf("view = %d after openZoom, want viewZoom (%d)", b.view, viewZoom)
	}

	// Press e — should transition to edit form.
	_, cmd := b.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})

	if b.view != viewForm {
		t.Errorf("view = %d after e in zoom, want viewForm (%d)", b.view, viewForm)
	}
	if b.zoom != nil {
		t.Error("zoom should be nil after transitioning to edit form")
	}
	if b.form == nil {
		t.Error("form should be set after e in zoom")
	}
	if cmd == nil {
		t.Error("e in zoom should return a textinputBlink command")
	}
}

func TestZoomOtherKey_Dismisses(t *testing.T) {
	b := newTestBoard(t)

	issue := makeIssue("bl-zoom-esc", "Zoom Dismiss", beadslite.StatusTodo)
	b.cols[b.focused].Blur()
	b.focused = colTodo
	b.cols[colTodo].Focus()
	b.cols[colTodo].SetItems([]list.Item{card{issue: issue}})

	b.openZoom()
	if b.view != viewZoom {
		t.Fatalf("view = %d after openZoom, want viewZoom (%d)", b.view, viewZoom)
	}

	// Press esc — should dismiss.
	b.Update(tea.KeyPressMsg{Code: tea.KeyEsc})

	if b.view != viewBoard {
		t.Errorf("view = %d after esc in zoom, want viewBoard (%d)", b.view, viewBoard)
	}
	if b.zoom != nil {
		t.Error("zoom should be nil after esc")
	}
}

func TestZoomScrollKey_DoesNotDismiss(t *testing.T) {
	b := newTestBoard(t)

	issue := makeIssue("bl-zoom-scroll", "Zoom Scroll", beadslite.StatusTodo)
	issue.Description = strings.Repeat("line\n", 100) // enough to overflow
	b.cols[b.focused].Blur()
	b.focused = colTodo
	b.cols[colTodo].Focus()
	b.cols[colTodo].SetItems([]list.Item{card{issue: issue}})

	b.openZoom()
	if b.view != viewZoom {
		t.Fatalf("view = %d after openZoom, want viewZoom (%d)", b.view, viewZoom)
	}

	// Press j — should scroll, not dismiss.
	b.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})

	if b.view != viewZoom {
		t.Errorf("view = %d after j in zoom, want viewZoom (%d)", b.view, viewZoom)
	}
	if b.zoom == nil {
		t.Error("zoom should still be active after scroll key")
	}
}

// --- vertical layout Up/Down column boundary navigation ---

// TestVerticalLayout_DownAtLastCardMovesFocusToNextColumn verifies that pressing
// Down at the last card of a column moves focus to the next column when in
// vertical layout mode.
func TestVerticalLayout_DownAtLastCardMovesFocusToNextColumn(t *testing.T) {
	b := newTestBoard(t)
	b.verticalLayout = true

	cardA := makeIssue("bl-a", "Card A", beadslite.StatusTodo)
	cardB := makeIssue("bl-b", "Card B", beadslite.StatusTodo)
	b.focused = colTodo
	b.cols[colTodo].Focus()
	b.cols[colTodo].SetItems([]list.Item{card{issue: cardA}, card{issue: cardB}})

	cardC := makeIssue("bl-c", "Card C", beadslite.StatusDoing)
	b.cols[colDoing].SetItems([]list.Item{card{issue: cardC}})

	// Select the last item in the Todo column.
	b.cols[colTodo].list.Select(1)

	// Press Down — should move focus to Doing column.
	b.Update(tea.KeyPressMsg{Code: tea.KeyDown})

	if b.focused != colDoing {
		t.Errorf("focused = %d after Down at last item in vertical mode, want colDoing (%d)", b.focused, colDoing)
	}
	// Cursor should be on the first card of the new column.
	if b.cols[colDoing].list.Index() != 0 {
		t.Errorf("cursor = %d after column advance, want 0 (first item)", b.cols[colDoing].list.Index())
	}
}

// TestVerticalLayout_UpAtFirstCardMovesFocusToPrevColumn verifies that pressing
// Up at the first card of a column moves focus to the previous column in
// vertical layout mode, landing on the last card.
func TestVerticalLayout_UpAtFirstCardMovesFocusToPrevColumn(t *testing.T) {
	b := newTestBoard(t)
	b.verticalLayout = true

	cardA := makeIssue("bl-a", "Card A", beadslite.StatusTodo)
	cardB := makeIssue("bl-b", "Card B", beadslite.StatusTodo)
	b.cols[colTodo].SetItems([]list.Item{card{issue: cardA}, card{issue: cardB}})

	cardC := makeIssue("bl-c", "Card C", beadslite.StatusDoing)
	cardD := makeIssue("bl-d", "Card D", beadslite.StatusDoing)
	b.cols[colDoing].Focus()
	b.focused = colDoing
	b.cols[colDoing].SetItems([]list.Item{card{issue: cardC}, card{issue: cardD}})

	// Cursor is already at index 0 in Doing column.
	b.cols[colDoing].list.Select(0)

	// Press Up — should move focus back to Todo column.
	b.Update(tea.KeyPressMsg{Code: tea.KeyUp})

	if b.focused != colTodo {
		t.Errorf("focused = %d after Up at first item in vertical mode, want colTodo (%d)", b.focused, colTodo)
	}
	// Cursor should be on the last card of the previous column.
	wantIdx := 1 // two items, last is index 1
	if b.cols[colTodo].list.Index() != wantIdx {
		t.Errorf("cursor = %d after column retreat, want %d (last item)", b.cols[colTodo].list.Index(), wantIdx)
	}
}

// TestHorizontalLayout_UpDownDelegateToColumn verifies that in horizontal layout
// mode, Up and Down fall through to the focused column without changing focus.
func TestHorizontalLayout_UpDownDelegateToColumn(t *testing.T) {
	b := newTestBoard(t)
	b.verticalLayout = false

	cardA := makeIssue("bl-a", "Card A", beadslite.StatusTodo)
	cardB := makeIssue("bl-b", "Card B", beadslite.StatusTodo)
	b.focused = colTodo
	b.cols[colTodo].Focus()
	b.cols[colTodo].SetItems([]list.Item{card{issue: cardA}, card{issue: cardB}})

	// Select last item — in horizontal mode Down should not change focus.
	b.cols[colTodo].list.Select(1)
	b.Update(tea.KeyPressMsg{Code: tea.KeyDown})

	if b.focused != colTodo {
		t.Errorf("focused = %d after Down in horizontal mode, want colTodo (%d)", b.focused, colTodo)
	}

	// Select first item — in horizontal mode Up should not change focus.
	b.cols[colTodo].list.Select(0)
	b.Update(tea.KeyPressMsg{Code: tea.KeyUp})

	if b.focused != colTodo {
		t.Errorf("focused = %d after Up in horizontal mode, want colTodo (%d)", b.focused, colTodo)
	}
}
