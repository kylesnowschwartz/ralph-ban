package main

import (
	"flag"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	beadslite "github.com/kylesnowschwartz/beads-lite"
)

// Version is set at build time via -ldflags "-X main.Version=x.y.z".
// Falls back to "dev" for local builds.
var Version = "dev"

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: ralph-ban [command] [flags]

Commands:
  (default)    open the TUI kanban board
  init         initialize a new project in the current directory
  claude       start a Claude Code orchestrator session
  version      print the current version
  update       update ralph-ban and bl to latest releases

Quick start:
  ralph-ban init --demo                          # new project with demo board
  ralph-ban                                     # open the board

Run the orchestrator:
  ralph-ban claude                              # batch mode (pauses for human merge approval)
  ralph-ban claude --auto                        # works until the board is empty
  ralph-ban claude --continue                   # continue most recent session
  ralph-ban claude --resume                     # interactive session picker
  ralph-ban claude --resume abc123              # resume specific session

Run 'ralph-ban <command> --help' for all flags.
`)
	}

	// Subcommand routing
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "init":
			runInit(os.Args[2:])
			return
		case "claude":
			runClaude(os.Args[2:])
			return
		case "version":
			fmt.Println(Version)
			return
		case "update":
			if err := runUpdate(os.Stdout); err != nil {
				fmt.Fprintf(os.Stderr, "update failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "snapshot":
			runSnapshot(os.Args[2:])
			return
		case "board":
			// Strip "board" from args so flag.Parse sees the right flags.
			os.Args = append(os.Args[:1], os.Args[2:]...)
		}
	}

	// Default: launch TUI board
	dump := flag.Bool("dump", false, "render one frame as JSON and exit")
	dumpZoom := flag.String("dump-zoom", "", "render zoom overlay for card ID and exit")
	width := flag.Int("width", 120, "terminal width for --dump/--dump-zoom")
	height := flag.Int("height", 40, "terminal height for --dump/--dump-zoom")
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

	if *dumpZoom != "" {
		if err := dumpZoomView(store, *dumpZoom, *width, *height, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "dump-zoom failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	b := newBoard(store)
	p := tea.NewProgram(b)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// runSnapshot handles the `ralph-ban snapshot` subcommand.
// --format json (default) writes structured JSON to stdout.
// --format ascii renders the board as plain text (no TUI required).
func runSnapshot(args []string) {
	fs := flag.NewFlagSet("snapshot", flag.ExitOnError)
	format := fs.String("format", "json", "output format: json or ascii")
	width := fs.Int("width", 120, "terminal width (ascii format only)")
	height := fs.Int("height", 40, "terminal height (ascii format only)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: ralph-ban snapshot [flags]\n\nExport the board state to stdout.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	dbPath := findDB()
	store, err := beadslite.NewStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	switch *format {
	case "json":
		if err := writeSnapshot(store, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "snapshot failed: %v\n", err)
			os.Exit(1)
		}
	case "ascii":
		if err := writeSnapshotASCII(store, *width, *height, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "snapshot ascii failed: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown format %q: use json or ascii\n", *format)
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
