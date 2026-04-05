package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestParseClaudeFlags exercises the full CLI parsing pipeline end-to-end:
// splitAtDoubleDash -> normalizeOptionalFlag -> flag.Parse -> fs.Visit -> buildClaudeArgs.
// Tests run in a temp dir without a plugin manifest so --plugin-dir is absent
// and we can assert exact arg lists. Plugin-dir is tested separately.
func TestParseClaudeFlags(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantArgs []string // exact claude args (order matters)
		wantName string   // expected agentName
		wantAuto bool     // expected auto mode
		wantPlan bool     // expected plan mode
		wantErr  bool
	}{
		{
			name:     "defaults (no positional prompt — agent has initialPrompt)",
			args:     nil,
			wantArgs: []string{"--agent", "rb-orchestrator"},
			wantName: "claude",
		},
		{
			name:     "custom prompt as flag",
			args:     []string{"--prompt", "do something"},
			wantArgs: []string{"--agent", "rb-orchestrator", "do something"},
			wantName: "claude",
		},
		{
			name:     "custom prompt as positional arg",
			args:     []string{"assess the board"},
			wantArgs: []string{"--agent", "rb-orchestrator", "assess the board"},
			wantName: "claude",
		},
		{
			name:     "model override",
			args:     []string{"--model", "sonnet"},
			wantArgs: []string{"--agent", "rb-orchestrator", "--model", "sonnet"},
			wantName: "claude",
		},
		{
			name:     "resume with ID",
			args:     []string{"--resume", "abc-123"},
			wantArgs: []string{"--resume", "abc-123"},
			wantName: "claude",
		},
		{
			name:     "resume without ID opens picker",
			args:     []string{"--resume"},
			wantArgs: []string{"--resume"},
			wantName: "claude",
		},
		{
			name:     "continue",
			args:     []string{"--continue"},
			wantArgs: []string{"--continue"},
			wantName: "claude",
		},
		{
			name:     "resume ignores custom prompt",
			args:     []string{"--resume", "abc-123", "--prompt", "ignored"},
			wantArgs: []string{"--resume", "abc-123"},
			wantName: "claude",
		},
		{
			name:     "continue ignores custom prompt",
			args:     []string{"--continue", "--prompt", "ignored"},
			wantArgs: []string{"--continue"},
			wantName: "claude",
		},
		{
			name:     "resume beats continue",
			args:     []string{"--resume", "abc-123", "--continue"},
			wantArgs: []string{"--resume", "abc-123"},
			wantName: "claude",
		},
		{
			name:     "passthrough flags",
			args:     []string{"--", "--dangerously-skip-permissions"},
			wantArgs: []string{"--agent", "rb-orchestrator", "--dangerously-skip-permissions"},
			wantName: "claude",
		},
		{
			name:     "resume with model and passthrough",
			args:     []string{"--resume", "abc-123", "--model", "sonnet", "--", "--verbose"},
			wantArgs: []string{"--resume", "abc-123", "--model", "sonnet", "--verbose"},
			wantName: "claude",
		},
		{
			name:     "custom agent name",
			args:     []string{"--name", "orchestrator-1"},
			wantName: "orchestrator-1",
		},
		{
			name:     "auto mode",
			args:     []string{"--auto"},
			wantName: "claude",
			wantAuto: true,
		},
		{
			name:     "plan mode defaults (no positional prompt — agent has initialPrompt)",
			args:     []string{"--plan"},
			wantArgs: []string{"--agent", "rb-planner"},
			wantName: "claude",
			wantPlan: true,
		},
		{
			name:     "plan mode with custom prompt",
			args:     []string{"--plan", "add card filtering"},
			wantArgs: []string{"--agent", "rb-planner", "add card filtering"},
			wantName: "claude",
			wantPlan: true,
		},
		{
			name:    "plan and auto are mutually exclusive",
			args:    []string{"--plan", "--auto"},
			wantErr: true,
		},
		{
			name:     "plan with resume skips agent",
			args:     []string{"--plan", "--resume", "abc-123"},
			wantArgs: []string{"--resume", "abc-123"},
			wantName: "claude",
		},
		{
			name:     "plan with continue skips agent",
			args:     []string{"--plan", "--continue"},
			wantArgs: []string{"--continue"},
			wantName: "claude",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Run in temp dir so --plugin-dir is absent (tested separately).
			t.Chdir(t.TempDir())

			session, err := parseClaudeFlags(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Exact arg list comparison when wantArgs is specified.
			if tt.wantArgs != nil {
				if len(session.claudeArgs) != len(tt.wantArgs) {
					t.Fatalf("args length = %d, want %d\n  got:  %v\n  want: %v",
						len(session.claudeArgs), len(tt.wantArgs), session.claudeArgs, tt.wantArgs)
				}
				for i := range tt.wantArgs {
					if session.claudeArgs[i] != tt.wantArgs[i] {
						t.Errorf("args[%d] = %q, want %q\n  full: %v", i, session.claudeArgs[i], tt.wantArgs[i], session.claudeArgs)
						break
					}
				}
			}

			if tt.wantName != "" && session.agentName != tt.wantName {
				t.Errorf("agentName = %q, want %q", session.agentName, tt.wantName)
			}
			if tt.wantAuto && !session.auto {
				t.Error("expected auto=true, got false")
			}
			if tt.wantPlan && !session.plan {
				t.Error("expected plan=true, got false")
			}
		})
	}
}

func TestSplitAtDoubleDash(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantBefore []string
		wantAfter  []string
	}{
		{
			name:       "no separator",
			args:       []string{"--model", "sonnet"},
			wantBefore: []string{"--model", "sonnet"},
		},
		{
			name:       "with separator",
			args:       []string{"--stop-mode", "batch", "--", "--dangerously-skip-permissions"},
			wantBefore: []string{"--stop-mode", "batch"},
			wantAfter:  []string{"--dangerously-skip-permissions"},
		},
		{
			name:      "empty before separator",
			args:      []string{"--", "--verbose"},
			wantAfter: []string{"--verbose"},
		},
		{
			name: "nil args",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before, after := splitAtDoubleDash(tt.args)
			if len(before) != len(tt.wantBefore) {
				t.Errorf("before = %v, want %v", before, tt.wantBefore)
			}
			if len(after) != len(tt.wantAfter) {
				t.Errorf("after = %v, want %v", after, tt.wantAfter)
			}
		})
	}
}

func TestBuildClaudeArgs_PluginDir(t *testing.T) {
	t.Run("present when extracted plugin exists", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)

		// Create the plugin manifest so buildClaudeArgs finds it.
		pluginDir := filepath.Join(dir, ralphBanDir, "plugin", ".claude-plugin")
		if err := os.MkdirAll(pluginDir, 0755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte(`{"name":"ralph-ban"}`), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		args := buildClaudeArgs("", "", "", false, false, false, nil)
		found := false
		for _, a := range args {
			if a == "--plugin-dir" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected --plugin-dir in args when plugin exists, got: %v", args)
		}
	})

	t.Run("absent when no extracted plugin", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)

		args := buildClaudeArgs("", "", "", false, false, false, nil)
		for _, a := range args {
			if a == "--plugin-dir" {
				t.Errorf("expected no --plugin-dir when plugin absent, got: %v", args)
				break
			}
		}
	})
}

func TestNormalizeOptionalFlag(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "bare flag at end",
			args: []string{"--resume"},
			want: []string{"--resume="},
		},
		{
			name: "bare flag before another flag",
			args: []string{"--resume", "--stop-mode", "batch"},
			want: []string{"--resume=", "--stop-mode", "batch"},
		},
		{
			name: "flag with value",
			args: []string{"--resume", "abc123"},
			want: []string{"--resume", "abc123"},
		},
		{
			name: "flag with equals syntax",
			args: []string{"--resume=abc123"},
			want: []string{"--resume=abc123"},
		},
		{
			name: "flag absent",
			args: []string{"--stop-mode", "batch"},
			want: []string{"--stop-mode", "batch"},
		},
		{
			name: "short form bare",
			args: []string{"-resume"},
			want: []string{"--resume="},
		},
		{
			name: "short form with value",
			args: []string{"-resume", "abc123"},
			want: []string{"-resume", "abc123"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeOptionalFlag(tt.args, "resume")
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("arg[%d] = %q, want %q (full: %v)", i, got[i], tt.want[i], got)
					break
				}
			}
		})
	}
}

func TestSetConfigField_CreatesNewFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config-dir")

	if err := setConfigField(dir, "stop_mode", "autonomous"); err != nil {
		t.Fatalf("setConfigField: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if cfg["stop_mode"] != "autonomous" {
		t.Errorf("stop_mode = %v, want autonomous", cfg["stop_mode"])
	}
}

func TestSetConfigField_PreservesExistingFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	// Write existing config with WIP limits.
	existing := `{"wip_limits": {"doing": 3}}`
	if err := os.WriteFile(path, []byte(existing), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := setConfigField(dir, "stop_mode", "batch"); err != nil {
		t.Fatalf("setConfigField: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if cfg["stop_mode"] != "batch" {
		t.Errorf("stop_mode = %v, want batch", cfg["stop_mode"])
	}
	// WIP limits should survive the merge.
	limits, ok := cfg["wip_limits"].(map[string]any)
	if !ok {
		t.Fatalf("wip_limits missing or wrong type after setConfigField")
	}
	if limits["doing"] != float64(3) {
		t.Errorf("wip_limits.doing = %v, want 3", limits["doing"])
	}
}
