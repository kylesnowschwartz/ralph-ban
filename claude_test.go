package main

import (
	"strings"
	"testing"
)

func TestBuildClaudeArgs(t *testing.T) {
	tests := []struct {
		name         string
		pluginDir    string
		settingsPath string
		model        string
		autonomous   bool
		teammateMode string
		prompt       string
		wantContains []string
		wantAbsent   []string
	}{
		{
			name:         "defaults",
			pluginDir:    "/project",
			settingsPath: "/project/.claude-plugin/settings.json",
			model:        "",
			autonomous:   false,
			teammateMode: "in-process",
			prompt:       "",
			wantContains: []string{
				"--plugin-dir", "/project",
				"--agent", "orchestrator",
				"--settings", "/project/.claude-plugin/settings.json",
				"--teammate-mode", "in-process",
				"State your role and mission",
			},
			wantAbsent: []string{
				"--model",
				"--dangerously-skip-permissions",
			},
		},
		{
			name:         "autonomous with model override",
			pluginDir:    "/project",
			settingsPath: "/project/.claude-plugin/settings.json",
			model:        "sonnet",
			autonomous:   true,
			teammateMode: "split-pane",
			prompt:       "",
			wantContains: []string{
				"--model", "sonnet",
				"--dangerously-skip-permissions",
				"--teammate-mode", "split-pane",
			},
		},
		{
			name:         "custom prompt",
			pluginDir:    "/project",
			settingsPath: "/project/.claude-plugin/settings.json",
			model:        "",
			autonomous:   false,
			teammateMode: "",
			prompt:       "Do something specific",
			wantContains: []string{"Do something specific"},
			wantAbsent:   []string{"State your role"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := buildClaudeArgs(tt.pluginDir, tt.settingsPath, tt.model, tt.autonomous, tt.teammateMode, tt.prompt)
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
