package main

import (
	"encoding/json"
	"os"
	"strings"
)

// wipConfig holds the per-column WIP limits loaded from .ralph-ban/config.json.
// A limit of 0 means unlimited. Absence of a config file also means unlimited.
type wipConfig struct {
	// WIPLimits maps lowercase column names to their WIP limits.
	// Keys match columnTitles (lowercased). Example: {"doing": 3, "review": 2}.
	WIPLimits map[string]int `json:"wip_limits"`
}

// loadConfig reads .ralph-ban/config.json and returns the parsed config.
// If the file does not exist the returned config has no limits (all zero).
// Any parse error is treated the same way — config is optional, so a broken
// file should not prevent the board from starting.
func loadConfig(dataDir string) wipConfig {
	path := dataDir + "/config.json"
	data, err := os.ReadFile(path)
	if err != nil {
		// File absent or unreadable — no limits.
		return wipConfig{}
	}

	var cfg wipConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		// Malformed JSON — no limits, fail open.
		return wipConfig{}
	}

	return cfg
}

// wipLimit returns the WIP limit for the given column index.
// Returns 0 if no limit is configured (unlimited).
func (c wipConfig) wipLimit(col columnIndex) int {
	if c.WIPLimits == nil {
		return 0
	}
	name := strings.ToLower(columnTitles[col])
	return c.WIPLimits[name]
}
