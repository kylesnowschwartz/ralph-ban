package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

	var cfg boardConfig
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

// --- installGitHooks ---

func TestInstallGitHooks_CreatesPostCheckout(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hooks")

	skipped, err := installGitHooks(dir)
	if err != nil {
		t.Fatalf("installGitHooks: %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("unexpected skipped hooks: %v", skipped)
	}

	hook := filepath.Join(dir, "post-checkout")
	info, err := os.Stat(hook)
	if err != nil {
		t.Fatalf("post-checkout not installed: %v", err)
	}
	if info.Mode()&0111 == 0 {
		t.Errorf("post-checkout not executable: %v", info.Mode())
	}

	data, err := os.ReadFile(hook)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), hookManaged) {
		t.Error("post-checkout missing managed marker")
	}
}

func TestInstallGitHooks_SkipsUserHook(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hooks")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Write a user-authored hook without the managed marker.
	userHook := []byte("#!/bin/bash\necho custom hook\n")
	hook := filepath.Join(dir, "post-checkout")
	if err := os.WriteFile(hook, userHook, 0755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	skipped, err := installGitHooks(dir)
	if err != nil {
		t.Fatalf("installGitHooks: %v", err)
	}
	if len(skipped) != 1 || skipped[0] != "post-checkout" {
		t.Errorf("skipped = %v, want [post-checkout]", skipped)
	}

	data, err := os.ReadFile(hook)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != string(userHook) {
		t.Error("installGitHooks overwrote user-authored hook")
	}
}

func TestInstallGitHooks_SkipsHookMentioningProjectName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hooks")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// A user hook that mentions ralph-ban but lacks the managed marker.
	userHook := []byte("#!/bin/bash\n# integrate with ralph-ban\necho custom\n")
	hook := filepath.Join(dir, "post-checkout")
	if err := os.WriteFile(hook, userHook, 0755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	skipped, err := installGitHooks(dir)
	if err != nil {
		t.Fatalf("installGitHooks: %v", err)
	}
	if len(skipped) != 1 {
		t.Errorf("skipped = %v, want [post-checkout]", skipped)
	}

	data, err := os.ReadFile(hook)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != string(userHook) {
		t.Error("installGitHooks overwrote user hook that mentions ralph-ban")
	}
}

func TestInstallGitHooks_OverwritesOwnHook(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hooks")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Write a stale hook with the managed marker.
	staleHook := []byte("#!/bin/bash\n# ralph-ban:managed — old version\necho stale\n")
	hook := filepath.Join(dir, "post-checkout")
	if err := os.WriteFile(hook, staleHook, 0755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	skipped, err := installGitHooks(dir)
	if err != nil {
		t.Fatalf("installGitHooks: %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("unexpected skipped hooks: %v", skipped)
	}

	data, err := os.ReadFile(hook)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) == string(staleHook) {
		t.Error("installGitHooks did not overwrite stale managed hook")
	}
	if !strings.Contains(string(data), hookManaged) {
		t.Error("updated hook missing managed marker")
	}
}

// --- extractPlugin ---

func TestExtractPlugin_CreatesFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "plugin")

	if err := extractPlugin(dir); err != nil {
		t.Fatalf("extractPlugin: %v", err)
	}

	// Plugin manifest must exist.
	manifest := filepath.Join(dir, ".claude-plugin", "plugin.json")
	if !fileExists(manifest) {
		t.Error("plugin.json not extracted")
	}

	// At least one agent must exist.
	orchestrator := filepath.Join(dir, "agents", "rb-orchestrator.md")
	if !fileExists(orchestrator) {
		t.Error("agents/rb-orchestrator.md not extracted")
	}

	// At least one hook script must exist and be executable.
	hookScript := filepath.Join(dir, "hooks", "session-start.sh")
	info, err := os.Stat(hookScript)
	if err != nil {
		t.Fatalf("hooks/session-start.sh not extracted: %v", err)
	}
	if info.Mode()&0111 == 0 {
		t.Errorf("hooks/session-start.sh not executable: %v", info.Mode())
	}
}

func TestExtractPlugin_OverwritesOnRerun(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "plugin")

	if err := extractPlugin(dir); err != nil {
		t.Fatalf("first extractPlugin: %v", err)
	}

	// Tamper with an extracted file.
	manifest := filepath.Join(dir, ".claude-plugin", "plugin.json")
	if err := os.WriteFile(manifest, []byte("tampered"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Re-extract should overwrite.
	if err := extractPlugin(dir); err != nil {
		t.Fatalf("second extractPlugin: %v", err)
	}

	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) == "tampered" {
		t.Error("extractPlugin did not overwrite tampered file")
	}
}

// --- defaultConfig ---

func TestDefaultConfig_IncludesProjectCommands(t *testing.T) {
	// Marshal defaultConfig and confirm project_commands key is present with
	// empty string values — this ensures the config template is visible to users.
	data, err := json.MarshalIndent(defaultConfig, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	cmdsRaw, ok := raw["project_commands"]
	if !ok {
		t.Fatal("defaultConfig JSON missing 'project_commands' key")
	}

	var cmds map[string]string
	if err := json.Unmarshal(cmdsRaw, &cmds); err != nil {
		t.Fatalf("Unmarshal project_commands: %v", err)
	}

	for _, field := range []string{"build", "test", "lint"} {
		val, exists := cmds[field]
		if !exists {
			t.Errorf("project_commands missing key %q", field)
			continue
		}
		if val != "" {
			t.Errorf("project_commands[%q] = %q, want empty string", field, val)
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

	var cfg boardConfig
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
