package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	beadslite "github.com/kylesnowschwartz/beads-lite"
)

// --- writeDefaultConfig ---

func TestWriteDefaultConfig_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	if err := writeDefaultConfig(path); err != nil {
		t.Fatalf("writeDefaultConfig: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var cfg wipConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if cfg.wipLimit(colDoing) != 3 {
		t.Errorf("doing limit = %d, want 3", cfg.wipLimit(colDoing))
	}
	if cfg.wipLimit(colReview) != 2 {
		t.Errorf("review limit = %d, want 2", cfg.wipLimit(colReview))
	}
}

func TestWriteDefaultConfig_ValidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	if err := writeDefaultConfig(path); err != nil {
		t.Fatalf("writeDefaultConfig: %v", err)
	}

	// loadConfig must parse it without error (it fails open on bad JSON,
	// so we check the limits directly).
	cfg := loadConfig(dir)
	if cfg.WIPLimits == nil {
		t.Error("loadConfig returned empty WIPLimits for freshly written config")
	}
}

// --- fileExists ---

func TestFileExists_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(path, []byte("hi"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if !fileExists(path) {
		t.Error("fileExists returned false for existing file")
	}
}

func TestFileExists_MissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nope.txt")
	if fileExists(path) {
		t.Error("fileExists returned true for missing file")
	}
}

func TestFileExists_Directory(t *testing.T) {
	dir := t.TempDir()
	if !fileExists(dir) {
		t.Error("fileExists returned false for existing directory")
	}
}

// --- seedStarterCards ---

func TestSeedStarterCards_CreatesExpectedCount(t *testing.T) {
	store, err := beadslite.NewStore(":memory:")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	count, err := seedStarterCards(store)
	if err != nil {
		t.Fatalf("seedStarterCards: %v", err)
	}

	if count != len(starterCards) {
		t.Errorf("seeded %d cards, want %d", count, len(starterCards))
	}

	issues, err := store.ListIssues()
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != len(starterCards) {
		t.Errorf("store has %d issues, want %d", len(issues), len(starterCards))
	}
}

func TestSeedStarterCards_AllInBacklog(t *testing.T) {
	store, err := beadslite.NewStore(":memory:")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	if _, err := seedStarterCards(store); err != nil {
		t.Fatalf("seedStarterCards: %v", err)
	}

	issues, err := store.ListIssues()
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}

	for _, issue := range issues {
		if issue.Status != beadslite.StatusBacklog {
			t.Errorf("starter card %q has status %q, want backlog", issue.Title, issue.Status)
		}
	}
}

func TestSeedStarterCards_PrioritySorting(t *testing.T) {
	store, err := beadslite.NewStore(":memory:")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	if _, err := seedStarterCards(store); err != nil {
		t.Fatalf("seedStarterCards: %v", err)
	}

	issues, err := store.ListIssues()
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}

	// Issues are ordered by priority ASC; starter cards are assigned P0, P1, P2.
	for i, issue := range issues {
		if issue.Priority != i {
			t.Errorf("issue[%d] priority = %d, want %d", i, issue.Priority, i)
		}
	}
}

// --- defaultConfig round-trip ---

func TestDefaultConfig_RoundTrip(t *testing.T) {
	// Marshal and unmarshal to confirm JSON round-trip is lossless.
	data, err := json.MarshalIndent(defaultConfig, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}

	var cfg wipConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if cfg.wipLimit(colDoing) != defaultConfig.wipLimit(colDoing) {
		t.Errorf("doing limit mismatch after round-trip: got %d, want %d",
			cfg.wipLimit(colDoing), defaultConfig.wipLimit(colDoing))
	}
	if cfg.wipLimit(colReview) != defaultConfig.wipLimit(colReview) {
		t.Errorf("review limit mismatch after round-trip: got %d, want %d",
			cfg.wipLimit(colReview), defaultConfig.wipLimit(colReview))
	}
}
