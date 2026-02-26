package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// runClaude starts a Claude Code session with the ralph-ban plugin loaded
// and the orchestrator agent. Claude Code reads agents/orchestrator.md
// directly, including its YAML frontmatter (model, name, isolation).
func runClaude(args []string) {
	fs := flag.NewFlagSet("claude", flag.ExitOnError)
	name := fs.String("name", "claude", "agent name (flows to hooks via CLAUDE_AGENT_NAME)")
	model := fs.String("model", "", "override the agent's default model (opus, sonnet, haiku)")
	autonomous := fs.Bool("autonomous", false, "skip permission prompts (dangerously-skip-permissions)")
	prompt := fs.String("prompt", "", "override the initial prompt sent to claude")
	resume := fs.String("resume", "", "resume a session by ID, or pass empty string for interactive picker")
	stopMode := fs.String("stop-mode", "", "stop hook mode: 'batch' (stop after dispatched work) or 'autonomous' (work until board empty)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: ralph-ban claude [flags]\n\nStart a Claude Code session with board orchestrator role.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	pluginDir, err := findPluginDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot locate plugin directory: %v\n", err)
		os.Exit(1)
	}

	claudeBin, err := exec.LookPath("claude")
	if err != nil {
		fmt.Fprintf(os.Stderr, "claude not found in PATH. Install Claude Code first.\n")
		os.Exit(1)
	}

	// Resolve the settings file path so hook commands work from any cwd.
	// The settings file uses $BL_ROOT to reference hook scripts, so it works
	// for workers in worktrees even though their cwd differs from the project root.
	settingsPath := filepath.Join(pluginDir, ".claude-plugin", "settings.json")

	// Write stop_mode to config before launching so the stop hook sees it immediately.
	if *stopMode != "" {
		if err := setConfigField(".ralph-ban", "stop_mode", *stopMode); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not write stop_mode to config: %v\n", err)
		}
	}

	claudeArgs := buildClaudeArgs(pluginDir, settingsPath, *model, *autonomous, *prompt, *resume)

	// Set agent name so hooks can identify this session.
	os.Setenv("CLAUDE_AGENT_NAME", *name)

	// Set BL_ROOT so workers in worktrees resolve the database from the project root.
	cwd, _ := os.Getwd()
	os.Setenv("BL_ROOT", cwd)

	// Replace this process with claude for clean signal handling.
	if err := syscall.Exec(claudeBin, append([]string{"claude"}, claudeArgs...), os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to exec claude: %v\n", err)
		os.Exit(1)
	}
}

// buildClaudeArgs constructs the argument list for the claude binary.
// The --agent flag delegates agent loading to Claude Code, which reads
// agents/orchestrator.md and applies its frontmatter (model, isolation, etc.).
// settingsPath is passed via --settings so hook commands resolve correctly
// for both the orchestrator and workers spawned in isolated worktrees.
func buildClaudeArgs(pluginDir, settingsPath, model string, autonomous bool, prompt, resume string) []string {
	args := []string{
		"--plugin-dir", pluginDir,
		"--settings", settingsPath,
	}

	// Resuming a session: pass --resume and skip --agent (the resumed session
	// already has its agent context). Also skip the default prompt.
	if resume != "" {
		args = append(args, "--resume", resume)
	} else {
		args = append(args, "--agent", "orchestrator")
	}

	// Only pass --model when explicitly overriding the agent's default.
	if model != "" {
		args = append(args, "--model", model)
	}

	if autonomous {
		args = append(args, "--dangerously-skip-permissions")
	}

	// Initial prompt as positional argument. Skipped when resuming —
	// the resumed session continues where it left off.
	if resume == "" {
		if prompt == "" {
			prompt = "State your role and mission, then assess the board and begin orchestration."
		}
		args = append(args, prompt)
	}

	return args
}

// findPluginDir locates the ralph-ban plugin directory by looking for
// .claude-plugin/plugin.json relative to the binary, then relative to cwd.
func findPluginDir() (string, error) {
	// Try relative to the binary location (works when built from this repo).
	binPath, err := os.Executable()
	if err == nil {
		binDir := filepath.Dir(binPath)
		candidate := filepath.Join(binDir, ".claude-plugin", "plugin.json")
		if _, err := os.Stat(candidate); err == nil {
			return binDir, nil
		}
	}

	// Try current working directory.
	cwd, err := os.Getwd()
	if err == nil {
		candidate := filepath.Join(cwd, ".claude-plugin", "plugin.json")
		if _, err := os.Stat(candidate); err == nil {
			return cwd, nil
		}
	}

	return "", fmt.Errorf("no .claude-plugin/plugin.json found near binary or in cwd")
}

// setConfigField reads .ralph-ban/config.json, sets a top-level field, and writes
// it back. Creates the directory and file if they don't exist. Preserves all
// existing fields (WIP limits, etc.) — only the named field is touched.
func setConfigField(dataDir, key, value string) error {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return err
	}

	path := filepath.Join(dataDir, "config.json")

	// Read existing config as a generic map to preserve unknown fields.
	cfg := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil {
		json.Unmarshal(data, &cfg) // ignore parse errors — overwrite with merged result
	}

	cfg[key] = value

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}
