# ralph-ban: TUI kanban board backed by beads-lite

bin       := "ralph-ban"
bl_bin    := "/usr/local/bin/bl"
bl_src    := "../beads-lite"

# List available recipes
default:
    @just --list

# Build ralph-ban
build:
    go build -o {{ bin }} .

# Build the beads-lite CLI
build-bl:
    cd {{ bl_src }} && go build -o {{ bl_bin }} ./cmd/bl

# Build both binaries
build-all: build build-bl

# Run Go unit tests
test:
    go test ./... -count=1

# Run Go tests for beads-lite
test-bl:
    cd {{ bl_src }} && go test ./... -count=1

# Run CLI integration tests
test-cli: build-all
    bash test_cli_integration.sh

# Run hook script tests
test-hooks: build-bl
    bash test_hooks.sh

# Run all tests
test-all: test test-cli test-hooks

# Dump the TUI as JSON (no TTY needed)
qa-dump *args: build
    ./{{ bin }} --dump {{ args }}

# Dump at a specific width (e.g. just dump-at 80)
qa-dump-at width="120" height="40": build
    ./{{ bin }} --dump --width {{ width }} --height {{ height }}

# Dump and pretty-print the board structure
qa-dump-board: build
    ./{{ bin }} --dump | jq '{focus, pan_offset, columns: [.columns[] | {title, cards: [.cards[] | {id, title, status}]}]}'

# Dump and display the rendered view text
qa-dump-view width="120" height="40": build
    ./{{ bin }} --dump --width {{ width }} --height {{ height }} | jq -r '.view'

# Launch the interactive TUI
run: build
    ./{{ bin }}

# Create a scratch board in a temp dir and launch the TUI
scratch: build-all
    #!/usr/bin/env bash
    set -euo pipefail
    dir=$(mktemp -d)
    cd "$dir"
    {{ bl_bin }} init
    {{ bl_bin }} create "Example task" --priority 1 --type feature
    {{ bl_bin }} create "Fix something" --priority 0 --type bug
    {{ bl_bin }} create "Write docs" --priority 3 --type task
    echo "Scratch board at: $dir"
    echo "Run 'just clean-scratch $dir' when done."
    BEADS_LITE_DB="$dir/.beads-lite/beads.db" {{ justfile_directory() }}/{{ bin }}

# Remove a scratch directory
clean-scratch dir:
    rm -rf {{ dir }}

# Run go vet and check formatting
lint:
    go vet ./...
    @gofmt -l . | grep . && echo "gofmt: files need formatting" && exit 1 || true

# Bump version (patch, minor, or major)
bump version:
    #!/usr/bin/env zsh
    set -e

    v=$(cat VERSION)
    IFS='.' read -r M m p <<< "$v"

    case {{version}} in
        patch) new="$M.$m.$((p+1))" ;;
        minor) new="$M.$((m+1)).0" ;;
        major) new="$((M+1)).0.0" ;;
        *) echo "Usage: just bump patch|minor|major" && exit 1 ;;
    esac

    echo "Bumping $v → $new"
    echo "$new" > VERSION
    git add VERSION
    echo "Version bumped to $new. Run 'just release' to commit, tag, and push."

# Commit, tag, and push the release
release notes="":
    #!/usr/bin/env zsh
    set -e

    v=$(cat VERSION)

    # Sanity checks
    if git tag -l "v$v" | grep -q .; then
        echo "Error: tag v$v already exists"
        exit 1
    fi

    behind=$(git rev-list HEAD..origin/main --count 2>/dev/null || echo 0)
    if [[ "$behind" -gt 0 ]]; then
        echo "Error: $behind commit(s) behind origin/main"
        echo "Run 'git pull --rebase' first"
        exit 1
    fi

    if git diff --cached --quiet; then
        echo "Error: nothing staged. Run 'just bump' first."
        exit 1
    fi

    # Commit, tag, push
    git commit -m "chore: bump version to $v"
    git tag "v$v"
    git push && git push --tags

    # Create GitHub Release
    notes="{{notes}}"
    if [[ -n "$notes" && -f "$notes" ]]; then
        gh release create "v$v" --title "v$v" --notes-file "$notes" --latest
    else
        gh release create "v$v" --title "v$v" --generate-notes --latest
    fi

    # Prime Go module proxy cache
    GOPROXY=https://proxy.golang.org go list -m "github.com/kylesnowschwartz/ralph-ban@v$v" || true
