package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	beadslite "github.com/kylesnowschwartz/beads-lite"
)

// hookManaged is the marker comment that identifies a git hook as ralph-ban managed.
// installGitHooks checks for this exact string before overwriting. Using a structured
// marker (not just "ralph-ban") avoids false positives on user hooks that mention
// the project name in comments or commands.
const hookManaged = "ralph-ban:managed"

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
	ProjectCommands:  ProjectCommands{},
	WorktreeSymlinks: defaultWorktreeSymlinks,
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
// If --demo is passed, a Conway's Game of Life project is seeded onto the board
// with cards in Todo so the orchestrator picks them up immediately. A CLAUDE.md
// is written to give the agent project context. Designed for throwaway directories
// where users can watch the full agent workflow end-to-end.
func runInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	demoFlag := fs.Bool("demo", false, "seed a demo project (Conway's Game of Life) onto the board")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: ralph-ban init [flags]\n\nInitialize a new ralph-ban project in the current directory.\nCreates .ralph-ban/ (config) and .beads-lite/ (database).\n\nFlags:\n")
		fs.PrintDefaults()
	}
	fs.Parse(args)
	demo := *demoFlag

	// --- Step 0: Verify git repo ---
	// Agent worktrees, hooks, and the post-checkout symlink chain all require git.
	// Fail early with a clear message rather than failing later in confusing ways.
	if _, err := os.Stat(".git"); os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "Error: not a git repository.")
		fmt.Fprintln(os.Stderr, "Run 'git init' first — ralph-ban needs git for agent worktrees and hooks.")
		os.Exit(1)
	}

	// --- Step 0b: Ensure .gitignore has correct entries ---
	// Symlinked directories in worktrees must be ignored WITHOUT trailing slashes.
	// Git's "pattern/" syntax only matches directories, not symlinks-to-directories.
	// The post-checkout hook creates symlinks for these in child worktrees; with
	// trailing slashes, git status shows them as untracked and the stop hook blocks.
	giFixed, giAdded := ensureGitignore()

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

	// --- Step 3: Optionally seed demo project ---
	seeded := 0
	if demo && !dbExisted {
		seeded, err = seedDemoCards(store)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to seed demo cards: %v\n", err)
			os.Exit(1)
		}
		if err := writeDemoCLAUDEmd(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to write CLAUDE.md: %v\n", err)
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

	// --- Step 6: Install git hooks ---
	// The post-checkout hook symlinks gitignored directories (.agent-history/,
	// .cloned-sources/, .ralph-ban/) into new worktrees so agents
	// have the same context as the main repo. Without this, workers in isolated
	// worktrees can't find reference material or project configuration.
	gitHooksInstalled := false
	hooksDir, err := resolveGitHooksDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: skipping git hooks: %v\n", err)
	} else if skipped, err := installGitHooks(hooksDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not install git hooks: %v\n", err)
	} else {
		gitHooksInstalled = true
		for _, name := range skipped {
			fmt.Fprintf(os.Stderr, "Warning: existing %s hook found — ralph-ban won't overwrite it.\n", name)
			fmt.Fprintf(os.Stderr, "  Agent worktrees won't have symlinks to gitignored directories.\n")
			fmt.Fprintf(os.Stderr, "  To fix: add the contents of githooks/%s to your existing hook.\n", name)
		}
	}

	// --- Step 7: Report results ---
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
			fmt.Printf("  %-24s  seeded %d demo cards in Todo\n", "", seeded)
			fmt.Printf("  %-24s  %s\n", "CLAUDE.md", "project context for agents")
		}
	}
	fmt.Printf("  %-24s  %s\n", pluginDir+"/", "hooks + agents extracted")
	fmt.Printf("  %-24s  %s\n", ".claude/agents/", "agents installed for --agent discovery")
	if gitHooksInstalled {
		fmt.Printf("  %-24s  %s\n", ".git/hooks/", "post-checkout hook for worktree symlinks")
	}
	if giFixed > 0 {
		fmt.Printf("  %-24s  fixed %d trailing-slash patterns (symlink compatibility)\n", ".gitignore", giFixed)
	}
	if giAdded > 0 {
		fmt.Printf("  %-24s  added %d missing entries\n", ".gitignore", giAdded)
	}

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
// The embedded FS (pluginFS) contains .claude-plugin/, _agents/, hooks/, and skills/.
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

	// Fix .gitignore before anything else — trailing-slash patterns don't
	// match symlinks, which is what worktrees create for these paths.
	if fixed, added := ensureGitignore(); fixed > 0 || added > 0 {
		fmt.Fprintf(os.Stderr, "Fixed .gitignore: %d trailing-slash patterns, %d new entries\n", fixed, added)
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
	hooksRefreshed := false
	if hooksDir, err := resolveGitHooksDir(); err == nil {
		if _, err := installGitHooks(hooksDir); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to refresh git hooks: %v\n", err)
		} else {
			hooksRefreshed = true
		}
	}
	if hooksRefreshed {
		fmt.Fprintf(os.Stderr, "Refreshed plugin, agents, and git hooks to %s\n", Version)
	} else {
		fmt.Fprintf(os.Stderr, "Refreshed plugin and agents to %s\n", Version)
	}
}

// gitignoreEntries lists paths that ralph-ban needs ignored. No trailing
// slashes — git's "pattern/" syntax only matches directories, not
// symlinks-to-directories. The post-checkout hook creates symlinks for
// these in child worktrees, so trailing slashes cause git status to show
// them as untracked.
var gitignoreEntries = []string{
	".ralph-ban",
	".beads-lite",
	".agent-history",
	".cloned-sources",
	".claude/agents",
}

// ensureGitignore appends missing entries to .gitignore, creating the file
// if needed. Also fixes legacy entries that use trailing slashes.
// Returns (fixed, added) counts so callers can report what changed.
func ensureGitignore() (fixed, added int) {
	const path = ".gitignore"

	existing, _ := os.ReadFile(path)
	lines := strings.Split(string(existing), "\n")

	// Build a set of existing patterns (trimmed).
	have := make(map[string]bool, len(lines))
	for _, l := range lines {
		have[strings.TrimSpace(l)] = true
	}

	// Fix trailing-slash variants: if ".foo/" is present but ".foo" is not,
	// replace the slash version so the pattern covers symlinks too.
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		for _, entry := range gitignoreEntries {
			if trimmed == entry+"/" && !have[entry] {
				lines[i] = strings.Replace(l, entry+"/", entry, 1)
				have[entry] = true
				fixed++
			}
		}
	}

	// Append any entries that are completely missing.
	var missing []string
	for _, entry := range gitignoreEntries {
		if !have[entry] && !have[entry+"/"] {
			missing = append(missing, entry)
		}
	}
	added = len(missing)

	if added == 0 && fixed == 0 {
		return
	}

	content := strings.Join(lines, "\n")
	if added > 0 {
		if len(content) > 0 && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += strings.Join(missing, "\n") + "\n"
	}

	os.WriteFile(path, []byte(content), 0644)
	return
}

// resolveGitHooksDir returns the directory where git looks for hooks.
// Checks core.hooksPath first (set by Husky, lefthook, or user config),
// falls back to .git/hooks. Returns an error if not inside a git repo.
func resolveGitHooksDir() (string, error) {
	// Verify we're in a git repo before touching .git/.
	if _, err := os.Stat(".git"); err != nil {
		return "", fmt.Errorf("not a git repository (no .git)")
	}

	out, err := exec.Command("git", "config", "core.hooksPath").Output()
	if err == nil {
		if p := strings.TrimSpace(string(out)); p != "" {
			return p, nil
		}
	}
	return ".git/hooks", nil
}

// installGitHooks writes embedded git hooks to the given hooks directory.
// Each hook is installed only if: (1) no hook exists at the path, or (2) the
// existing hook contains the hookManaged marker, indicating we own it.
// User-authored hooks (without the marker) are never overwritten — instead
// the hook name is returned in the skipped slice so callers can warn.
func installGitHooks(hooksDir string) (skipped []string, err error) {
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		return nil, fmt.Errorf("create hooks dir: %w", err)
	}

	entries, err := gitHooksFS.ReadDir("githooks")
	if err != nil {
		return nil, fmt.Errorf("read embedded githooks: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}

		data, err := gitHooksFS.ReadFile(filepath.Join("githooks", e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read embedded %s: %w", e.Name(), err)
		}

		dest := filepath.Join(hooksDir, e.Name())

		// Check for existing hook we don't own.
		if existing, err := os.ReadFile(dest); err == nil {
			if !strings.Contains(string(existing), hookManaged) {
				skipped = append(skipped, e.Name())
				continue
			}
		}

		if err := os.WriteFile(dest, data, 0755); err != nil {
			return skipped, fmt.Errorf("write %s: %w", e.Name(), err)
		}
	}
	return skipped, nil
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

// demoCards defines the Conway's Game of Life project seeded by `ralph-ban init --demo`.
// Cards are placed in Todo so the orchestrator picks them up immediately. Each card
// carries specs (acceptance criteria) so agents know when the work is done.
var demoCards = []struct {
	title       string
	issueType   beadslite.IssueType
	description string
	specs       []string
}{
	{
		title:     "Initialize Go project",
		issueType: beadslite.IssueTypeTask,
		description: `Set up the Go module and entry point for the Game of Life CLI.

The project should compile and run from the start — even if it only prints "hello" initially.`,
		specs: []string{
			"go.mod exists with a module name",
			"main.go compiles with go build",
			"Running the binary prints output to stdout",
		},
	},
	{
		title:     "Implement grid and cell state",
		issueType: beadslite.IssueTypeFeature,
		description: `Create the core data structure: a 2D grid of cells, each alive or dead.

The grid wraps at edges (toroidal topology) so gliders and other patterns don't crash into walls.
Include a function to randomly populate the grid for initial state.`,
		specs: []string{
			"Grid struct with configurable width and height",
			"Cells are alive or dead (boolean)",
			"Grid wraps at edges (toroidal)",
			"Random population function seeds the grid",
		},
	},
	{
		title:     "Implement Game of Life rules",
		issueType: beadslite.IssueTypeFeature,
		description: `Apply Conway's rules to compute the next generation from the current grid.

Rules:
- A live cell with 2 or 3 neighbors survives
- A dead cell with exactly 3 neighbors becomes alive
- All other cells die or stay dead

This should be a pure function: takes a grid, returns a new grid. No mutation of the input.`,
		specs: []string{
			"Pure function: input grid -> output grid (no mutation)",
			"Live cell with 2-3 neighbors survives",
			"Dead cell with exactly 3 neighbors is born",
			"All other cells die or stay dead",
			"Neighbor count respects toroidal wrapping",
		},
	},
	{
		title:     "Terminal renderer with tick loop",
		issueType: beadslite.IssueTypeFeature,
		description: `Render the grid to the terminal and animate generations in a loop.

Use ANSI escape codes to clear the screen between frames. Display a generation counter.
The tick speed should be configurable (default ~200ms). Ctrl-C exits cleanly.`,
		specs: []string{
			"ANSI clear screen between frames",
			"Alive and dead cells use distinct characters",
			"Generation counter displayed",
			"Configurable tick speed (default 200ms)",
			"Ctrl-C exits cleanly",
		},
	},
	{
		title:     "CLI flags for configuration",
		issueType: beadslite.IssueTypeFeature,
		description: `Add command-line flags so users can configure the simulation without editing code.

Use Go's standard flag package.`,
		specs: []string{
			"--width flag (default 40)",
			"--height flag (default 20)",
			"--speed flag in milliseconds (default 200)",
			"--pattern flag to select a preset (default random)",
			"--help shows usage",
		},
	},
	{
		title:     "Preset patterns",
		issueType: beadslite.IssueTypeFeature,
		description: `Add classic Game of Life patterns that can be selected via the --pattern flag.

Place each pattern centered in the grid. If the grid is too small, warn and exit.`,
		specs: []string{
			"Glider pattern",
			"Blinker pattern",
			"Pulsar pattern",
			"Patterns are centered in the grid",
			"--pattern random fills randomly (default behavior)",
		},
	},
	{
		title:     "README with usage instructions",
		issueType: beadslite.IssueTypeTask,
		description: `Write a README.md covering how to build, run, and configure the Game of Life CLI.

Include example commands and a brief description of what the project does.`,
		specs: []string{
			"Build instructions (go build)",
			"Usage examples with flags",
			"List of available patterns",
			"Brief project description",
		},
	},
}

// seedDemoCards creates the demo project cards in the store and returns how many were created.
func seedDemoCards(store *beadslite.Store) (int, error) {
	for i, dc := range demoCards {
		issue := beadslite.NewIssue(dc.title)
		issue.Status = beadslite.StatusTodo
		issue.Priority = min(i, 4) // P0-P4 (capped); later cards share P4 but sort by creation order
		issue.Type = dc.issueType
		issue.Description = dc.description
		for _, spec := range dc.specs {
			issue.Specifications = append(issue.Specifications, beadslite.Spec{Text: spec})
		}
		if err := store.CreateIssue(issue); err != nil {
			return i, fmt.Errorf("create demo card %q: %w", dc.title, err)
		}
	}
	return len(demoCards), nil
}

// demoCLAUDEmd is the CLAUDE.md content written by --demo to give agents project context.
const demoCLAUDEmd = `# Conway's Game of Life CLI

Go command-line program that simulates Conway's Game of Life in the terminal.

## What to build

An animated terminal program that:
- Displays a grid of cells (alive/dead) using text characters
- Applies Conway's rules each generation
- Clears and redraws the terminal each tick
- Accepts CLI flags for grid size, speed, and preset patterns

## How to build and run

` + "```" + `
go build -o life .
./life                          # random 40x20 grid
./life --width 60 --height 30   # custom size
./life --pattern glider          # preset pattern
./life --speed 100               # faster ticks (ms)
` + "```" + `

## Design guidance

- Keep it simple. Standard library only — no TUI frameworks.
- The grid wraps at edges (toroidal topology).
- The step function should be pure: takes a grid, returns a new grid.
- Use ANSI escape codes for screen clearing, not a TUI library.
`

// writeDemoCLAUDEmd writes the demo project's CLAUDE.md to the current directory.
// Only called during --demo init to give agents context about what they're building.
func writeDemoCLAUDEmd() error {
	return os.WriteFile("CLAUDE.md", []byte(demoCLAUDEmd), 0644)
}
