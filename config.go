package main

import (
	"encoding/json"
	"os"
	"strings"
)

// ProjectCommands holds the project-specific shell commands for build, test, and lint.
// When a field is empty the worker skips that step with a warning rather than failing.
// Commands are read from .ralph-ban/config.json so the worker template stays
// language-agnostic — a Go project uses GOWORK=off go build while a Node.js
// project could use npm run build without touching any agent template.
type ProjectCommands struct {
	Build string `json:"build"`
	Test  string `json:"test"`
	Lint  string `json:"lint"`
}

// boardConfig holds board-level configuration loaded from .ralph-ban/config.json.
// All fields are optional — a missing or empty config file is silently treated
// as all-defaults so a broken config never prevents the board from starting.
type boardConfig struct {
	// WIPLimits maps lowercase column names to their WIP limits.
	// Keys match columnTitles (lowercased). Example: {"doing": 3, "review": 2}.
	// A limit of 0 means unlimited.
	WIPLimits map[string]int `json:"wip_limits"`

	// ProjectCommands holds the shell commands workers should run to build,
	// test, and lint the project. Empty strings mean "skip that step".
	ProjectCommands ProjectCommands `json:"project_commands"`
}

// loadConfig reads .ralph-ban/config.json and returns the parsed config.
// If the file does not exist the returned config has no limits (all zero).
// Any parse error is treated the same way — config is optional, so a broken
// file should not prevent the board from starting.
func loadConfig(dataDir string) boardConfig {
	path := dataDir + "/config.json"
	data, err := os.ReadFile(path)
	if err != nil {
		// File absent or unreadable — no limits.
		return boardConfig{}
	}

	var cfg boardConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		// Malformed JSON — no limits, fail open.
		return boardConfig{}
	}

	return cfg
}

// wipLimit returns the WIP limit for the given column index.
// Returns 0 if no limit is configured (unlimited).
func (c boardConfig) wipLimit(col columnIndex) int {
	if c.WIPLimits == nil {
		return 0
	}
	name := strings.ToLower(columnTitles[col])
	return c.WIPLimits[name]
}
