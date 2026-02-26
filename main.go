package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	beadslite "github.com/kylesnowschwartz/beads-lite"
)

func main() {
	// Subcommand routing: ralph-ban init | ralph-ban claude | ralph-ban board | ralph-ban [flags]
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "init":
			runInit(os.Args[2:])
			return
		case "claude":
			runClaude(os.Args[2:])
			return
		case "board":
			// Strip "board" from args so flag.Parse sees the right flags.
			os.Args = append(os.Args[:1], os.Args[2:]...)
		}
	}

	// Default: launch TUI board
	dump := flag.Bool("dump", false, "render one frame as JSON and exit")
	width := flag.Int("width", 120, "terminal width for --dump")
	height := flag.Int("height", 40, "terminal height for --dump")
	flag.Parse()

	dbPath := findDB()

	store, err := beadslite.NewStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	if *dump {
		if err := dumpBoard(store, *width, *height, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "dump failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	b := newBoard(store)
	p := tea.NewProgram(b, tea.WithAltScreen(), tea.WithMouseCellMotion())

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// findDB locates the beads-lite database, searching upward from cwd.
func findDB() string {
	// Check for BEADS_LITE_DB environment variable first
	if path := os.Getenv("BEADS_LITE_DB"); path != "" {
		return path
	}

	// Look for .beads-lite/beads.db in current directory
	path := ".beads-lite/beads.db"
	if _, err := os.Stat(path); err == nil {
		return path
	}

	// Fall back to creating in current directory
	fmt.Fprintln(os.Stderr, "No .beads-lite/beads.db found. Run 'bl init' first.")
	os.Exit(1)
	return ""
}
