package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildClaudeArgs(t *testing.T) {
	tests := []struct {
		name         string
		model        string
		prompt       string
		resume       string
		passthrough  []string
		wantContains []string
		wantAbsent   []string
	}{
		{
			name: "defaults",
			wantContains: []string{
				"--agent", "orchestrator",
				"State your role and mission",
			},
			wantAbsent: []string{
				"--model",
				// --plugin-dir presence depends on whether .ralph-ban/plugin/ exists
				// on disk — tested separately in TestBuildClaudeArgs_PluginDir.
				"--settings",
				"--dangerously-skip-permissions",
				"--resume",
			},
		},
		{
			name:         "model override",
			model:        "sonnet",
			wantContains: []string{"--model", "sonnet"},
		},
		{
			name:         "passthrough flags",
			passthrough:  []string{"--dangerously-skip-permissions"},
			wantContains: []string{"--dangerously-skip-permissions"},
		},
		{
			name:         "custom prompt",
			prompt:       "Do something specific",
			wantContains: []string{"Do something specific"},
			wantAbsent:   []string{"State your role"},
		},
		{
			name:   "resume skips agent and prompt",
			resume: "abc-123-session-id",
			wantContains: []string{
				"--resume", "abc-123-session-id",
			},
			wantAbsent: []string{
				"--agent",
				"State your role",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := buildClaudeArgs(tt.model, tt.prompt, tt.resume, tt.passthrough)
			joined := strings.Join(args, " ")

			t.Logf("args: %v", args)
			t.Logf("joined: %s", joined)

			for _, want := range tt.wantContains {
				found := false
				for _, a := range args {
					if strings.Contains(a, want) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected args to contain %q, got: %v", want, args)
				}
			}

			for _, absent := range tt.wantAbsent {
				for _, a := range args {
					if strings.Contains(a, absent) {
						t.Errorf("expected args NOT to contain %q, got: %v", absent, args)
						break
					}
				}
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

		args := buildClaudeArgs("", "", "", nil)
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

		args := buildClaudeArgs("", "", "", nil)
		for _, a := range args {
			if a == "--plugin-dir" {
				t.Errorf("expected no --plugin-dir when plugin absent, got: %v", args)
				break
			}
		}
	})
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
