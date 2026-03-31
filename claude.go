package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// claudeSession holds the parsed result of CLI flag processing.
// Extracted from runClaude so the full parsing pipeline
// (splitAtDoubleDash -> normalizeOptionalFlag -> flag.Parse -> fs.Visit -> buildClaudeArgs)
// can be tested end-to-end without exec.
type claudeSession struct {
	claudeArgs []string
	agentName  string
	auto       bool
	plan       bool
}

// parseClaudeFlags processes raw CLI args through the full parsing pipeline.
// This is the testable core of runClaude — everything except exec and env setup.
// Uses ContinueOnError so --help returns flag.ErrHelp instead of calling os.Exit.
func parseClaudeFlags(args []string) (*claudeSession, error) {
	flagArgs, passthrough := splitAtDoubleDash(args)

	fs := flag.NewFlagSet("claude", flag.ContinueOnError)
	name := fs.String("name", "claude", "agent name (flows to hooks via CLAUDE_AGENT_NAME)")
	model := fs.String("model", "", "override the agent's default model (opus, sonnet, haiku)")
	prompt := fs.String("prompt", "", "initial prompt (also accepted as positional arg)")
	resume := fs.String("resume", "", "resume a session by ID, or empty string for picker")
	cont := fs.Bool("continue", false, "continue the most recent session")
	auto := fs.Bool("auto", false, "autonomous mode: drain the board without pausing")
	plan := fs.Bool("plan", false, "planning mode: brainstorm, spec, and decompose work into board cards")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: ralph-ban claude [flags] [prompt] [-- claude-flags...]

Start a Claude Code session with the board orchestrator or planner loaded.
Flags before -- are ralph-ban's; flags after -- pass through to claude.

Flags:
`)
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
Examples:
  ralph-ban claude                              # default orchestrator session
  ralph-ban claude --auto                        # drain the board without intervention
  ralph-ban claude "assess the board"           # custom prompt
  ralph-ban claude --continue                    # continue most recent session
  ralph-ban claude --resume                     # interactive session picker
  ralph-ban claude --resume abc123              # resume specific session
  ralph-ban claude -- --dangerously-skip-permissions  # pass flags to claude

Plan work:
  ralph-ban claude --plan                         # interactive planning session
  ralph-ban claude --plan "add card filtering"    # plan a specific feature
`)
	}

	if err := fs.Parse(normalizeOptionalFlag(flagArgs, "resume")); err != nil {
		return nil, err
	}

	// Detect whether --resume was explicitly passed. Go's flag package
	// can't distinguish "not passed" from "passed with empty string" via
	// the pointer value alone — both give "". fs.Visit only iterates
	// flags that were explicitly set, so this is the reliable check.
	resumeSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "resume" {
			resumeSet = true
		}
	})

	// --plan and --auto are mutually exclusive: planner explores, orchestrator executes.
	if *auto && *plan {
		return nil, fmt.Errorf("--plan and --auto are mutually exclusive (planner explores, orchestrator executes)")
	}

	// Positional arg after flags = prompt (mirrors claude's own interface).
	if fs.NArg() > 0 && *prompt == "" {
		*prompt = fs.Arg(0)
	}

	return &claudeSession{
		claudeArgs: buildClaudeArgs(*model, *prompt, *resume, resumeSet, *cont, *plan, passthrough),
		agentName:  *name,
		auto:       *auto,
		plan:       *plan,
	}, nil
}

// runClaude starts a Claude Code session with the orchestrator agent.
// The ralph-ban plugin must be installed via Claude Code's plugin system;
// hooks, agents, and settings are resolved from the plugin cache automatically.
// BL_ROOT is set to cwd so hooks can find the project's beads-lite database.
//
// Flags before -- are ralph-ban's; flags after -- pass through to claude.
// Example: ralph-ban claude --auto -- --dangerously-skip-permissions
func runClaude(args []string) {
	session, err := parseClaudeFlags(args)
	if err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Refresh plugin/agents if the binary is newer than what's extracted.
	// This keeps projects in sync after `ralph-ban update` without manual init.
	ensurePlugin()

	claudeBin, err := exec.LookPath("claude")
	if err != nil {
		fmt.Fprintf(os.Stderr, "claude not found in PATH. Install Claude Code first.\n")
		os.Exit(1)
	}

	// Set agent name so hooks can identify this session.
	os.Setenv("CLAUDE_AGENT_NAME", session.agentName)

	// Set stop mode as env var so hooks see it for this session only.
	// --auto maps to "autonomous"; absence falls through to config file or "batch" default.
	if session.auto {
		os.Setenv("RALPH_BAN_STOP_MODE", "autonomous")
	}

	// Plan mode: tell hooks to skip workflow gates. The planner reads code and
	// creates board cards — it doesn't own the working tree, dispatch workers,
	// or manage board lifecycle. The stop-guard and board-sync hooks are irrelevant.
	if session.plan {
		os.Setenv("RALPH_BAN_PLAN_MODE", "1")
	}

	// Disable optional git index locks for the entire session. Read-only git
	// commands (status, diff) opportunistically refresh the index, taking an
	// exclusive lock. Claude Code, hooks, and LSP all run git concurrently,
	// causing frequent .git/index.lock collisions. GIT_OPTIONAL_LOCKS=0 skips
	// the optional refresh; write commands (add, commit, merge) still lock
	// correctly via mandatory locks.
	os.Setenv("GIT_OPTIONAL_LOCKS", "0")

	// Set BL_ROOT so workers in worktrees resolve the database from the project root.
	// Git traversal handles subdirectory invocation; falls back to cwd for non-git dirs.
	os.Setenv("BL_ROOT", projectRoot())

	// Replace this process with claude for clean signal handling.
	if err := syscall.Exec(claudeBin, append([]string{"claude"}, session.claudeArgs...), os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to exec claude: %v\n", err)
		os.Exit(1)
	}
}

// buildClaudeArgs constructs the argument list for the claude binary.
// --plugin-dir points Claude Code at the extracted plugin under .ralph-ban/plugin/,
// which contains hooks, agents, and settings. This replaces the old approach of
// installing via `claude plugin marketplace add` / `claude plugin install`.
// If the extracted plugin doesn't exist (old installs), --plugin-dir is skipped
// and Claude Code falls back to the plugin cache.
//
// Three mutually exclusive session modes:
//   - new (default): loads --agent rb-orchestrator (or rb-planner when plan=true)
//   - resume (resumeSet): passes --resume [id] to continue a previous session
//   - continue (cont): passes --continue to pick up the most recent session
//
// passthrough args are appended last — these come from after -- in the CLI.
func buildClaudeArgs(model, prompt, resume string, resumeSet, cont, plan bool, passthrough []string) []string {
	var args []string

	// Load plugin from extracted directory if it exists.
	pluginDir := filepath.Join(projectRoot(), ralphBanDir, "plugin")
	pluginManifest := filepath.Join(pluginDir, ".claude-plugin", "plugin.json")
	if fileExists(pluginManifest) {
		args = append(args, "--plugin-dir", pluginDir)
	}

	// Existing session: pass through the resume/continue flag and skip --agent
	// and the default prompt (the session already has its agent context).
	existingSession := resumeSet || cont
	switch {
	case resumeSet:
		if resume != "" {
			args = append(args, "--resume", resume)
		} else {
			args = append(args, "--resume")
		}
	case cont:
		args = append(args, "--continue")
	default:
		if plan {
			args = append(args, "--agent", "rb-planner")
		} else {
			args = append(args, "--agent", "rb-orchestrator")
		}
	}

	// Only pass --model when explicitly overriding the agent's default.
	if model != "" {
		args = append(args, "--model", model)
	}

	// Pass through any claude-native flags (e.g. --dangerously-skip-permissions).
	args = append(args, passthrough...)

	// Initial prompt as positional argument. Skipped for existing sessions —
	// they continue where they left off.
	if !existingSession {
		if prompt == "" {
			if plan {
				prompt = "Read the board state and codebase context, then ask what I'd like to plan."
			} else {
				prompt = "State your role and mission, then assess the board and begin orchestration."
			}
		}
		args = append(args, prompt)
	}

	return args
}

// normalizeOptionalFlag rewrites a bare --flag (no value) to --flag= so Go's
// flag.String parser accepts it. Without this, `--resume` without a value
// produces "flag needs an argument" because flag.String always expects one.
//
// The rule: if --flag or -flag appears and the next arg starts with "-" or
// is absent, the flag has no value and we rewrite it to --flag=.
// If the flag already uses "=" syntax (--flag=value), it's left alone.
func normalizeOptionalFlag(args []string, name string) []string {
	long := "--" + name
	short := "-" + name
	prefix := long + "="
	shortPrefix := short + "="

	out := make([]string, len(args))
	copy(out, args)

	for i, a := range out {
		// Already has explicit value via "=".
		if strings.HasPrefix(a, prefix) || strings.HasPrefix(a, shortPrefix) {
			continue
		}
		if a == long || a == short {
			// Check if the next arg looks like a value (not another flag).
			if i+1 >= len(out) || strings.HasPrefix(out[i+1], "-") {
				out[i] = long + "="
			}
		}
	}
	return out
}

// splitAtDoubleDash splits args at the first "--" separator.
// Args before -- are returned as first; args after as second.
// If no -- is present, all args go to first and second is nil.
func splitAtDoubleDash(args []string) (before, after []string) {
	for i, a := range args {
		if a == "--" {
			return args[:i], args[i+1:]
		}
	}
	return args, nil
}

// projectRoot returns the git repository root, falling back to cwd.
// Mirrors the same logic hooks use in lib/board-state.sh.
func projectRoot() string {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	cwd, _ := os.Getwd()
	return cwd
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

	// Atomic write: write to temp file, then rename. os.Rename is atomic on
	// POSIX, so a crash mid-write leaves the original file intact.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
