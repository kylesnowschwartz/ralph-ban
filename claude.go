package main

import (
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
	teammateMode := fs.String("teammate-mode", "in-process", "teammate display mode (in-process, split-pane, auto)")
	prompt := fs.String("prompt", "", "override the initial prompt sent to claude")
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

	claudeArgs := buildClaudeArgs(pluginDir, *model, *autonomous, *teammateMode, *prompt)

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
func buildClaudeArgs(pluginDir, model string, autonomous bool, teammateMode, prompt string) []string {
	args := []string{
		"--plugin-dir", pluginDir,
		"--agent", "orchestrator",
	}

	// Only pass --model when explicitly overriding the agent's default.
	if model != "" {
		args = append(args, "--model", model)
	}

	if autonomous {
		args = append(args, "--dangerously-skip-permissions")
	}

	if teammateMode != "" {
		args = append(args, "--teammate-mode", teammateMode)
	}

	// Initial prompt as positional argument.
	if prompt == "" {
		prompt = "State your role and mission, then assess the board and begin orchestration."
	}
	args = append(args, prompt)

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
