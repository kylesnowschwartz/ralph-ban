package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	beadslite "github.com/kylesnowschwartz/beads-lite"
)

const (
	ralphBanDir = ".ralph-ban"
	beadsDir    = ".beads-lite"
	dbName      = "beads.db"
)

// defaultConfig is written to .ralph-ban/config.json on init.
// WIP limits of 0 mean unlimited — these are sensible starting suggestions,
// not enforced policy. ProjectCommands is included with empty strings so users
// see the structure and can fill in their own build/test/lint commands without
// needing to know the JSON schema.
var defaultConfig = boardConfig{
	WIPLimits: map[string]int{
		"doing":  3,
		"review": 2,
	},
	ProjectCommands: ProjectCommands{},
}

// runInit bootstraps a new ralph-ban project in the current directory.
//
// It creates:
//   - .ralph-ban/           — TUI configuration directory
//   - .ralph-ban/config.json — WIP limits and other board configuration
//   - .beads-lite/           — beads-lite data directory
//   - .beads-lite/beads.db   — SQLite database (schema initialized)
//
// If .beads-lite/beads.db already exists, the existing database is adopted
// rather than replaced. This lets projects that already run `bl init` start
// using the TUI without disrupting their data.
//
// If --seed is passed, a small set of starter cards is created in Backlog so
// the board opens with something visible instead of empty columns.
func runInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	seedFlag := fs.Bool("seed", false, "create starter cards in Backlog")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: ralph-ban init [flags]\n\nInitialize a new ralph-ban project in the current directory.\nCreates .ralph-ban/ (config) and .beads-lite/ (database).\n\nFlags:\n")
		fs.PrintDefaults()
	}
	fs.Parse(args)
	seed := *seedFlag

	// --- Step 1: Create .ralph-ban/ config directory ---
	if err := os.MkdirAll(ralphBanDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create %s: %v\n", ralphBanDir, err)
		os.Exit(1)
	}

	configPath := filepath.Join(ralphBanDir, "config.json")
	configCreated := false
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if err := writeDefaultConfig(configPath); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to write config: %v\n", err)
			os.Exit(1)
		}
		configCreated = true
	}

	// --- Step 2: Create .beads-lite/ database ---
	if err := os.MkdirAll(beadsDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create %s: %v\n", beadsDir, err)
		os.Exit(1)
	}

	dbPath := filepath.Join(beadsDir, dbName)
	dbExisted := fileExists(dbPath)

	store, err := beadslite.NewStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize database: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	// Write default beads-lite config if absent. This mirrors what `bl init`
	// does — ralph-ban init creates the DB directly (not via bl init), so it
	// must also ensure the config file exists.
	blConfigPath := filepath.Join(beadsDir, "config.json")
	if _, err := os.Stat(blConfigPath); os.IsNotExist(err) {
		blCfg := beadslite.Config{RequireSpecsForReview: func() *bool { v := true; return &v }()}
		if data, err := json.MarshalIndent(blCfg, "", "  "); err == nil {
			data = append(data, '\n')
			os.WriteFile(blConfigPath, data, 0644)
		}
	}

	// --- Step 3: Optionally seed starter cards ---
	seeded := 0
	if seed && !dbExisted {
		seeded, err = seedStarterCards(store)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to seed starter cards: %v\n", err)
			os.Exit(1)
		}
	}

	// --- Step 4: Extract Claude Code plugin ---
	pluginDir := filepath.Join(ralphBanDir, "plugin")
	if err := extractPlugin(pluginDir); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to extract plugin: %v\n", err)
		os.Exit(1)
	}

	// --- Step 5: Install agents for --agent discovery ---
	// claude --agent <name> resolves through: .claude/agents/ > ~/.claude/agents/ > plugins.
	// Plugin agents are only found for subagent dispatch (Agent tool), not for --agent.
	// Copy agents to .claude/agents/ so `ralph-ban claude` (which passes --agent orchestrator)
	// works in any project, not just repos that happen to have agents/ at their root.
	if err := installAgents(filepath.Join(pluginDir, "agents"), ".claude/agents"); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to install agents: %v\n", err)
		os.Exit(1)
	}

	// --- Step 6: Report results ---
	fmt.Println("Initialized ralph-ban:")
	fmt.Printf("  %-24s  %s\n", ralphBanDir+"/", "board configuration")
	if configCreated {
		fmt.Printf("  %-24s  %s\n", configPath, "WIP limits: doing=3, review=2")
	} else {
		fmt.Printf("  %-24s  %s\n", configPath, "(already exists, kept as-is)")
	}
	fmt.Printf("  %-24s  %s\n", beadsDir+"/", "task database")
	if dbExisted {
		fmt.Printf("  %-24s  %s\n", dbPath, "(existing database adopted)")
	} else {
		fmt.Printf("  %-24s  %s\n", dbPath, "(new database created)")
		if seeded > 0 {
			fmt.Printf("  %-24s  seeded %d starter cards in Backlog\n", "", seeded)
		}
	}
	fmt.Printf("  %-24s  %s\n", pluginDir+"/", "hooks + agents extracted")
	fmt.Printf("  %-24s  %s\n", ".claude/agents/", "agents installed for --agent discovery")

	fmt.Println()
	fmt.Println("Run 'ralph-ban' to open the board.")
	if !dbExisted {
		fmt.Println("Run 'bl create \"task title\"' to add cards from the CLI.")
	}
}

// installAgents copies agent markdown files from srcDir to destDir.
// This bridges the gap between plugin agents (found by the Agent tool) and
// --agent discovery (which only searches .claude/agents/ and ~/.claude/agents/).
// Without this, `ralph-ban claude` (which passes --agent orchestrator) would
// only work in repos that happen to have agents/ at their root.
func installAgents(srcDir, destDir string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return fmt.Errorf("read agents dir: %w", err)
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}

	// Remove previously installed rb-* agents so renames don't leave stale files.
	// Only our rb- namespace is touched — user agents are preserved.
	if existing, err := os.ReadDir(destDir); err == nil {
		for _, e := range existing {
			if strings.HasPrefix(e.Name(), "rb-") && strings.HasSuffix(e.Name(), ".md") {
				os.Remove(filepath.Join(destDir, e.Name()))
			}
		}
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(srcDir, e.Name()))
		if err != nil {
			return fmt.Errorf("read agent %s: %w", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(destDir, e.Name()), data, 0644); err != nil {
			return fmt.Errorf("write agent %s: %w", e.Name(), err)
		}
	}
	return nil
}

// extractPlugin writes the embedded plugin files to destDir.
// The embedded FS (pluginFS) contains .claude-plugin/, _agents/, and hooks/.
// _agents/ is remapped to agents/ in the output so the plugin has the standard
// structure Claude Code expects. The underscore prefix in the source keeps agent
// files out of Claude Code's discovery chain during development.
//
// The entire destDir is removed first so files deleted between versions
// (e.g. settings.json after hooks moved to hooks/hooks.json) don't linger.
// This is safe because destDir is fully managed by ralph-ban — user
// configuration lives in .ralph-ban/config.json, outside the plugin tree.
//
// A .version file is written last so ensurePlugin can detect stale extractions.
func extractPlugin(destDir string) error {
	os.RemoveAll(destDir)

	if err := fs.WalkDir(pluginFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Remap _agents/ → agents/ so the plugin output has the standard name.
		outPath := strings.Replace(path, "_agents", "agents", 1)
		target := filepath.Join(destDir, outPath)

		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}

		data, err := pluginFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", path, err)
		}

		// Shell scripts need executable permission.
		perm := os.FileMode(0644)
		if strings.HasSuffix(path, ".sh") {
			perm = 0755
		}

		return os.WriteFile(target, data, perm)
	}); err != nil {
		return err
	}

	// Stamp the extraction so ensurePlugin can detect stale versions.
	return os.WriteFile(filepath.Join(destDir, ".version"), []byte(Version+"\n"), 0644)
}

// ensurePlugin re-extracts the plugin and agents if the binary version is newer
// than what was last extracted. Called on every `ralph-ban claude` launch so
// projects stay in sync after `ralph-ban update` without manual `init`.
func ensurePlugin() {
	pluginDir := filepath.Join(ralphBanDir, "plugin")
	versionFile := filepath.Join(pluginDir, ".version")

	// Read the stamp. Missing or mismatched = needs refresh.
	data, err := os.ReadFile(versionFile)
	if err == nil && strings.TrimSpace(string(data)) == Version {
		return // already current
	}

	// Re-extract plugin and install agents. Errors are non-fatal — the session
	// can still work with stale agents, and init will fix it later.
	if err := extractPlugin(pluginDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to refresh plugin: %v\n", err)
		return
	}
	if err := installAgents(filepath.Join(pluginDir, "agents"), ".claude/agents"); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to refresh agents: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "Refreshed plugin and agents to %s\n", Version)
}

// writeDefaultConfig serializes defaultConfig to the given path.
func writeDefaultConfig(path string) error {
	data, err := json.MarshalIndent(defaultConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	// Append newline for clean file ending.
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

// fileExists returns true if path exists on disk (file or directory).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// starterCards defines the seed data created when running `ralph-ban init --seed`.
// These are placed in Backlog so the user triages them deliberately.
var starterCards = []struct {
	title       string
	description string
}{
	{
		title:       "Add your first task",
		description: "Press 'n' to create a new card, or run 'bl create \"task title\"' from the CLI.",
	},
	{
		title:       "Move a card to Todo",
		description: "Select a card and press 'l' (or right arrow) to move it right across columns.",
	},
	{
		title:       "Edit a card",
		description: "Select a card and press 'e' to open the edit form. Press Enter to save, Esc to cancel.",
	},
}

// seedStarterCards creates the starter cards in the store and returns how many were created.
func seedStarterCards(store *beadslite.Store) (int, error) {
	for i, sc := range starterCards {
		issue := beadslite.NewIssue(sc.title)
		issue.Status = beadslite.StatusBacklog
		issue.Priority = i // P0, P1, P2 so they sort top-to-bottom
		issue.Description = sc.description
		if err := store.CreateIssue(issue); err != nil {
			return i, fmt.Errorf("create starter card %q: %w", sc.title, err)
		}
	}
	return len(starterCards), nil
}
