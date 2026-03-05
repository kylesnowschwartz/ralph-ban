package main

import (
	"os"
	"path/filepath"
	"testing"

	"charm.land/bubbles/v2/list"
	beadslite "github.com/kylesnowschwartz/beads-lite"
)

// --- boardConfig.wipLimit ---

func TestWIPLimit_NoConfig(t *testing.T) {
	cfg := boardConfig{}
	for i := columnIndex(0); i < numColumns; i++ {
		if got := cfg.wipLimit(i); got != 0 {
			t.Errorf("wipLimit(%d) = %d, want 0 (no config)", i, got)
		}
	}
}

func TestWIPLimit_ConfiguredColumns(t *testing.T) {
	cfg := boardConfig{
		WIPLimits: map[string]int{
			"doing":  3,
			"review": 2,
		},
	}

	tests := []struct {
		col  columnIndex
		want int
	}{
		{colBacklog, 0},
		{colTodo, 0},
		{colDoing, 3},
		{colReview, 2},
		{colDone, 0},
	}

	for _, tt := range tests {
		if got := cfg.wipLimit(tt.col); got != tt.want {
			t.Errorf("wipLimit(%d) = %d, want %d", tt.col, got, tt.want)
		}
	}
}

// --- loadConfig ---

func TestLoadConfig_MissingFile(t *testing.T) {
	// Directory that doesn't contain config.json
	dir := t.TempDir()
	cfg := loadConfig(dir)

	// Missing file means no limits — board starts without WIP enforcement.
	for i := columnIndex(0); i < numColumns; i++ {
		if got := cfg.wipLimit(i); got != 0 {
			t.Errorf("wipLimit(%d) = %d with missing config, want 0", i, got)
		}
	}
}

func TestLoadConfig_ValidJSON(t *testing.T) {
	dir := t.TempDir()
	content := `{"wip_limits": {"doing": 3, "review": 2}}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := loadConfig(dir)

	if got := cfg.wipLimit(colDoing); got != 3 {
		t.Errorf("doing limit = %d, want 3", got)
	}
	if got := cfg.wipLimit(colReview); got != 2 {
		t.Errorf("review limit = %d, want 2", got)
	}
	if got := cfg.wipLimit(colTodo); got != 0 {
		t.Errorf("todo limit = %d, want 0 (not configured)", got)
	}
}

func TestLoadConfig_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`not valid json`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Malformed JSON should silently produce no limits (fail open).
	cfg := loadConfig(dir)
	for i := columnIndex(0); i < numColumns; i++ {
		if got := cfg.wipLimit(i); got != 0 {
			t.Errorf("wipLimit(%d) = %d with malformed config, want 0", i, got)
		}
	}
}

func TestLoadConfig_EmptyLimits(t *testing.T) {
	dir := t.TempDir()
	content := `{"wip_limits": {}}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := loadConfig(dir)
	for i := columnIndex(0); i < numColumns; i++ {
		if got := cfg.wipLimit(i); got != 0 {
			t.Errorf("wipLimit(%d) = %d with empty limits, want 0", i, got)
		}
	}
}

// --- ProjectCommands ---

func TestProjectCommands_EmptyByDefault(t *testing.T) {
	// A config with no project_commands field should give zero-value commands.
	dir := t.TempDir()
	content := `{"wip_limits": {"doing": 3}}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := loadConfig(dir)
	if cfg.ProjectCommands.Build != "" {
		t.Errorf("Build = %q, want empty", cfg.ProjectCommands.Build)
	}
	if cfg.ProjectCommands.Test != "" {
		t.Errorf("Test = %q, want empty", cfg.ProjectCommands.Test)
	}
	if cfg.ProjectCommands.Lint != "" {
		t.Errorf("Lint = %q, want empty", cfg.ProjectCommands.Lint)
	}
}

func TestProjectCommands_LoadedFromConfig(t *testing.T) {
	dir := t.TempDir()
	content := `{
		"wip_limits": {"doing": 3},
		"project_commands": {
			"build": "GOWORK=off go build ./...",
			"test":  "GOWORK=off go test ./... -count=1",
			"lint":  "GOWORK=off go vet ./..."
		}
	}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := loadConfig(dir)
	if got := cfg.ProjectCommands.Build; got != "GOWORK=off go build ./..." {
		t.Errorf("Build = %q, want %q", got, "GOWORK=off go build ./...")
	}
	if got := cfg.ProjectCommands.Test; got != "GOWORK=off go test ./... -count=1" {
		t.Errorf("Test = %q, want %q", got, "GOWORK=off go test ./... -count=1")
	}
	if got := cfg.ProjectCommands.Lint; got != "GOWORK=off go vet ./..." {
		t.Errorf("Lint = %q, want %q", got, "GOWORK=off go vet ./...")
	}
}

func TestProjectCommands_PartialConfig(t *testing.T) {
	// Only build specified — test and lint remain empty (worker skips them).
	dir := t.TempDir()
	content := `{"project_commands": {"build": "make build"}}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := loadConfig(dir)
	if got := cfg.ProjectCommands.Build; got != "make build" {
		t.Errorf("Build = %q, want %q", got, "make build")
	}
	if cfg.ProjectCommands.Test != "" {
		t.Errorf("Test = %q, want empty when not specified", cfg.ProjectCommands.Test)
	}
	if cfg.ProjectCommands.Lint != "" {
		t.Errorf("Lint = %q, want empty when not specified", cfg.ProjectCommands.Lint)
	}
}

// --- specsRequiredForReview ---

func TestSpecsRequiredForReview_NilDefaultsTrue(t *testing.T) {
	cfg := boardConfig{}
	if !cfg.specsRequiredForReview() {
		t.Error("nil RequireSpecsForReview should default to true")
	}
}

func TestSpecsRequiredForReview_ExplicitTrue(t *testing.T) {
	cfg := boardConfig{RequireSpecsForReview: boolPtr(true)}
	if !cfg.specsRequiredForReview() {
		t.Error("explicit true should return true")
	}
}

func TestSpecsRequiredForReview_ExplicitFalse(t *testing.T) {
	cfg := boardConfig{RequireSpecsForReview: boolPtr(false)}
	if cfg.specsRequiredForReview() {
		t.Error("explicit false should return false")
	}
}

func TestSpecsRequiredForReview_JSONRoundTrip(t *testing.T) {
	dir := t.TempDir()
	content := `{"wip_limits": {}, "require_specs_for_review": false}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := loadConfig(dir)
	if cfg.RequireSpecsForReview == nil {
		t.Fatal("RequireSpecsForReview should be non-nil after loading JSON with the field")
	}
	if cfg.specsRequiredForReview() {
		t.Error("require_specs_for_review: false should round-trip as false")
	}
}

// --- handleMove WIP enforcement ---

func newTestBoardWithWIP(t *testing.T, cfg boardConfig) *board {
	t.Helper()
	store := newTestStore(t)
	b := newBoard(store)
	b.wip = cfg
	// Apply limits to columns to match what newBoard would do with a real config.
	for i := columnIndex(0); i < numColumns; i++ {
		b.cols[i].wipLimit = cfg.wipLimit(i)
	}
	b.termWidth = 120
	b.termHeight = 40
	b.loaded = true
	b.resizeColumns()
	return b
}

func TestHandleMove_BlockedByWIPLimit(t *testing.T) {
	cfg := boardConfig{WIPLimits: map[string]int{"doing": 1}}
	b := newTestBoardWithWIP(t, cfg)

	// Fill Doing to capacity (1 card).
	existing := makeIssue("bl-existing", "Already Doing", beadslite.StatusDoing)
	b.cols[colDoing].SetItems([]list.Item{card{issue: existing}})

	// Attempt to move another card from Todo into Doing.
	moving := makeIssue("bl-moving", "Try To Enter", beadslite.StatusTodo)
	b.focused = colTodo
	b.cols[colTodo].Focus()
	b.cols[colTodo].SetItems([]list.Item{card{issue: moving}})

	cmd := b.handleMove(moveMsg{
		card:   card{issue: moving},
		source: colTodo,
		target: colDoing,
	})

	if cmd != nil {
		t.Error("handleMove should return nil when WIP limit is exceeded")
	}
	if b.err == nil {
		t.Fatal("handleMove should set b.err when WIP limit is exceeded")
	}

	// The card must not have moved.
	doingItems := b.cols[colDoing].list.Items()
	if len(doingItems) != 1 {
		t.Errorf("doing has %d items after blocked move, want 1 (only existing card)", len(doingItems))
	}
}

func TestHandleMove_AllowedWhenUnderWIPLimit(t *testing.T) {
	cfg := boardConfig{WIPLimits: map[string]int{"doing": 2}}
	b := newTestBoardWithWIP(t, cfg)

	// Doing has 1 card; limit is 2 — move should succeed.
	existing := makeIssue("bl-existing", "Already Doing", beadslite.StatusDoing)
	if err := b.store.CreateIssue(existing); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	b.cols[colDoing].SetItems([]list.Item{card{issue: existing}})

	moving := makeIssue("bl-moving", "Enter Doing", beadslite.StatusTodo)
	if err := b.store.CreateIssue(moving); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	b.focused = colTodo
	b.cols[colTodo].Focus()
	b.cols[colTodo].SetItems([]list.Item{card{issue: moving}})

	cmd := b.handleMove(moveMsg{
		card:   card{issue: moving},
		source: colTodo,
		target: colDoing,
	})

	if cmd == nil {
		t.Error("handleMove should return a persist command when under WIP limit")
	}
	if b.err != nil {
		t.Errorf("handleMove should clear b.err on successful move, got: %v", b.err)
	}

	doingItems := b.cols[colDoing].list.Items()
	if len(doingItems) != 2 {
		t.Errorf("doing has %d items, want 2", len(doingItems))
	}
}

func TestHandleMove_NoLimitMeansUnlimited(t *testing.T) {
	// No WIP config — column accepts any number of cards.
	b := newTestBoard(t)

	for i := 0; i < 10; i++ {
		issue := makeIssue("bl-flood-"+string(rune('a'+i)), "Card", beadslite.StatusDoing)
		if err := b.store.CreateIssue(issue); err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		items := b.cols[colDoing].list.Items()
		b.cols[colDoing].SetItems(append(items, card{issue: issue}))
	}

	extra := makeIssue("bl-extra", "Extra Card", beadslite.StatusTodo)
	if err := b.store.CreateIssue(extra); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	b.focused = colTodo
	b.cols[colTodo].Focus()
	b.cols[colTodo].SetItems([]list.Item{card{issue: extra}})

	cmd := b.handleMove(moveMsg{
		card:   card{issue: extra},
		source: colTodo,
		target: colDoing,
	})

	if cmd == nil {
		t.Error("handleMove should allow move when no WIP limit is configured")
	}
	if b.err != nil {
		t.Errorf("no WIP error expected, got: %v", b.err)
	}
}
