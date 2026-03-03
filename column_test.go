package main

import (
	"bytes"
	"os"
	"testing"
	"time"

	"charm.land/bubbles/v2/list"
	beadslite "github.com/kylesnowschwartz/beads-lite"
)

// TestMain runs all tests. In lipgloss v2, color profiles are per-renderer
// and auto-detected; there is no global SetColorProfile.
func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

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

func TestNewColumnStartsBlurred(t *testing.T) {
	c := newColumn(colTodo)

	if c.Focused() {
		t.Error("newColumn should start blurred (not focused)")
	}
}

func TestFocusBlurSwapsDelegate(t *testing.T) {
	c := newColumn(colDoing)

	c.Focus()
	if !c.Focused() {
		t.Error("Focus() should set focused state")
	}

	c.Blur()
	if c.Focused() {
		t.Error("Blur() should clear focused state")
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

// Age bucket tests

func TestCardAgeBucketFresh(t *testing.T) {
	// A card updated 10 minutes ago is fresh.
	updatedAt := time.Now().Add(-10 * time.Minute)
	got := cardAgeBucket(updatedAt)
	if got != ageFresh {
		t.Errorf("cardAgeBucket(10min ago) = %d, want ageFresh (%d)", got, ageFresh)
	}
}

func TestCardAgeBucketAgingJustOver1Day(t *testing.T) {
	// A card updated 25 hours ago is aging (1–3 days).
	updatedAt := time.Now().Add(-25 * time.Hour)
	got := cardAgeBucket(updatedAt)
	if got != ageAging {
		t.Errorf("cardAgeBucket(25h ago) = %d, want ageAging (%d)", got, ageAging)
	}
}

func TestCardAgeBucketAgingJustUnder3Days(t *testing.T) {
	// A card updated 71 hours ago is still aging (not yet stale).
	updatedAt := time.Now().Add(-71 * time.Hour)
	got := cardAgeBucket(updatedAt)
	if got != ageAging {
		t.Errorf("cardAgeBucket(71h ago) = %d, want ageAging (%d)", got, ageAging)
	}
}

func TestCardAgeBucketStale(t *testing.T) {
	// A card updated 4 days ago is stale.
	updatedAt := time.Now().Add(-4 * 24 * time.Hour)
	got := cardAgeBucket(updatedAt)
	if got != ageStale {
		t.Errorf("cardAgeBucket(4 days ago) = %d, want ageStale (%d)", got, ageStale)
	}
}

func TestCardAgeBucketBoundaryExactly1Day(t *testing.T) {
	// Exactly 24 hours ago crosses into aging.
	updatedAt := time.Now().Add(-24 * time.Hour)
	got := cardAgeBucket(updatedAt)
	if got != ageAging {
		t.Errorf("cardAgeBucket(exactly 24h ago) = %d, want ageAging (%d)", got, ageAging)
	}
}

func TestCardAgeBucketBoundaryExactly3Days(t *testing.T) {
	// Exactly 72 hours ago crosses into stale.
	updatedAt := time.Now().Add(-72 * time.Hour)
	got := cardAgeBucket(updatedAt)
	if got != ageStale {
		t.Errorf("cardAgeBucket(exactly 72h ago) = %d, want ageStale (%d)", got, ageStale)
	}
}

func TestCardAgeBucketZeroTime(t *testing.T) {
	// A zero time.Time (the Go default for unset time fields) should classify
	// as stale rather than fresh — unknown age is safer treated as old.
	got := cardAgeBucket(time.Time{})
	if got != ageStale {
		t.Errorf("cardAgeBucket(zero time) = %d, want ageStale (%d)", got, ageStale)
	}
}

// Age-aware delegate rendering tests

// makeCard creates a card with a specific UpdatedAt for testing.
func makeCard(updatedAt time.Time) card {
	return card{issue: &beadslite.Issue{
		ID:        "bl-test",
		Title:     "Test Card",
		Priority:  1,
		Type:      beadslite.IssueTypeTask,
		UpdatedAt: updatedAt,
	}}
}

// makeListModel builds a minimal list.Model sized for delegate rendering.
func makeListModel(item list.Item) list.Model {
	m := list.New([]list.Item{item}, list.NewDefaultDelegate(), 40, 10)
	return m
}

func TestAgeAwareDelegateFreshUsesDefaultColors(t *testing.T) {
	// A fresh card should render without any age-tint override.
	// We verify by comparing output: a fresh card through the age-aware
	// delegate should produce the same output as a plain default delegate.
	cd := makeCard(time.Now().Add(-1 * time.Hour))
	m := makeListModel(cd)

	defaultDel := newBlurredDelegate()
	ageDel := newBlurredAgeDelegate()

	var defaultBuf, ageBuf bytes.Buffer
	defaultDel.Render(&defaultBuf, m, 0, cd)
	ageDel.Render(&ageBuf, m, 0, cd)

	if defaultBuf.String() != ageBuf.String() {
		t.Errorf("fresh card: age-aware delegate output differs from default\ngot:  %q\nwant: %q", ageBuf.String(), defaultBuf.String())
	}
}

func TestAgeAwareDelegateAgingCardDiffersFromFresh(t *testing.T) {
	// An aging card should render with a different (amber-tinted) title,
	// so the output should differ from that of a fresh card.
	freshCard := makeCard(time.Now().Add(-1 * time.Hour))
	agingCard := makeCard(time.Now().Add(-48 * time.Hour))
	m := makeListModel(freshCard)

	ageDel := newBlurredAgeDelegate()

	var freshBuf, agingBuf bytes.Buffer
	ageDel.Render(&freshBuf, m, 0, freshCard)
	ageDel.Render(&agingBuf, m, 0, agingCard)

	if freshBuf.String() == agingBuf.String() {
		t.Error("aging card should render differently from a fresh card")
	}
}

func TestAgeAwareDelegateStaleCardDiffersFromAging(t *testing.T) {
	// A stale card should render with a different (orange-red) title color
	// compared to an aging card.
	agingCard := makeCard(time.Now().Add(-48 * time.Hour))
	staleCard := makeCard(time.Now().Add(-5 * 24 * time.Hour))
	m := makeListModel(agingCard)

	ageDel := newBlurredAgeDelegate()

	var agingBuf, staleBuf bytes.Buffer
	ageDel.Render(&agingBuf, m, 0, agingCard)
	ageDel.Render(&staleBuf, m, 0, staleCard)

	if agingBuf.String() == staleBuf.String() {
		t.Error("stale card should render differently from an aging card")
	}
}

func TestDoneColumnUsesNoAgingDelegate(t *testing.T) {
	// We can't directly inspect the delegate type from outside the list model,
	// but we can verify that the plain focused delegate (used by Done) renders
	// stale and fresh cards identically — since it ignores age entirely.
	staleCard := makeCard(time.Now().Add(-10 * 24 * time.Hour))
	freshCard := makeCard(time.Now().Add(-1 * time.Hour))

	plainDel := newFocusedDelegate()
	m := makeListModel(staleCard)

	var staleBuf, freshBuf bytes.Buffer
	plainDel.Render(&staleBuf, m, 0, staleCard)
	plainDel.Render(&freshBuf, m, 0, freshCard)

	// Plain delegate ignores age — both should produce the same output.
	if staleBuf.String() != freshBuf.String() {
		t.Errorf("plain delegate should render stale and fresh cards identically\ngot stale: %q\ngot fresh: %q", staleBuf.String(), freshBuf.String())
	}
}

func TestDoneColumnFocusDoesNotProduceAgeTint(t *testing.T) {
	// Verify Done column doesn't apply age tinting: the ageAwareDelegate
	// would tint a stale card, but the Done column's plain delegate should not.
	staleCard := makeCard(time.Now().Add(-10 * 24 * time.Hour))
	m := makeListModel(staleCard)

	plainDel := newFocusedDelegate()
	ageDel := newFocusedAgeDelegate()

	var plainBuf, ageBuf bytes.Buffer
	plainDel.Render(&plainBuf, m, 0, staleCard)
	ageDel.Render(&ageBuf, m, 0, staleCard)

	// The age-aware delegate should tint; the plain one should not.
	// They should produce different output for a stale card.
	if plainBuf.String() == ageBuf.String() {
		t.Error("age-aware delegate should tint stale cards, but output matched plain delegate")
	}
}
