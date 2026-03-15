package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// githubRelease holds the fields we need from the GitHub releases API.
type githubRelease struct {
	TagName string `json:"tag_name"`
}

// runUpdate checks for newer releases of ralph-ban and bl, downloads and
// replaces each binary that is out of date, then refreshes the embedded plugin.
func runUpdate(w io.Writer) error {
	// Locate the current ralph-ban executable (resolve symlinks so we replace
	// the real binary, not the link).
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate ralph-ban executable: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("resolve symlink for ralph-ban: %w", err)
	}

	updatedRalphBan, err := updateBinary(w, "ralph-ban", "kylesnowschwartz/ralph-ban", "ralph-ban", exePath, Version)
	if err != nil {
		return fmt.Errorf("ralph-ban update: %w", err)
	}

	// Locate bl — skip with a warning if it isn't on PATH.
	blPath, err := exec.LookPath("bl")
	if err != nil {
		fmt.Fprintf(w, "Warning: bl not found on PATH — skipping bl update\n")
	} else {
		blPath, err = filepath.EvalSymlinks(blPath)
		if err != nil {
			fmt.Fprintf(w, "Warning: resolve symlink for bl: %v — skipping bl update\n", err)
		} else {
			blVer := blVersion(blPath)
			if _, err := updateBinary(w, "bl", "kylesnowschwartz/beads-lite", "bl", blPath, blVer); err != nil {
				return fmt.Errorf("bl update: %w", err)
			}
		}
	}

	// After ralph-ban self-updates, refresh the embedded plugin so hooks and
	// agents stay in sync with the new binary.
	if updatedRalphBan {
		pluginDir := filepath.Join(ralphBanDir, "plugin")
		if err := extractPlugin(pluginDir); err != nil {
			fmt.Fprintf(w, "Warning: failed to refresh plugin: %v\n", err)
		}
		if err := installAgents(filepath.Join(pluginDir, "agents"), ".claude/agents"); err != nil {
			fmt.Fprintf(w, "Warning: failed to refresh agents: %v\n", err)
		}
	}

	return nil
}

// updateBinary checks whether binaryName (from owner/repo) has a release newer
// than current, and if so downloads and replaces the binary at destPath.
// currentVer is the installed version of the specific binary being updated.
// Returns true if the binary was updated.
func updateBinary(w io.Writer, displayName, repo, binaryName, destPath, currentVer string) (bool, error) {
	latest, err := latestRelease(repo)
	if err != nil {
		return false, fmt.Errorf("fetch latest release: %w", err)
	}

	latestVer := strings.TrimPrefix(latest, "v")
	currentVer = strings.TrimPrefix(currentVer, "v")

	if currentVer != "" && latestVer == currentVer {
		fmt.Fprintf(w, "%s is already up to date (%s)\n", displayName, currentVer)
		return false, nil
	}

	if currentVer != "" {
		fmt.Fprintf(w, "Updating %s: %s -> %s\n", displayName, currentVer, latestVer)
	} else {
		fmt.Fprintf(w, "Updating %s to %s\n", displayName, latestVer)
	}

	if err := downloadAndReplace(repo, latest, binaryName, destPath); err != nil {
		return false, err
	}

	fmt.Fprintf(w, "Updated %s to %s\n", displayName, latestVer)
	return true, nil
}

// blVersion runs `bl version` and parses the version string.
// Returns empty string if bl can't report its version.
func blVersion(blPath string) string {
	out, err := exec.Command(blPath, "version").Output()
	if err != nil {
		return ""
	}
	// Output format: "bl version 1.5.1"
	parts := strings.Fields(strings.TrimSpace(string(out)))
	if len(parts) >= 3 {
		return parts[2]
	}
	return ""
}

// latestRelease returns the tag_name of the latest release for owner/repo.
func latestRelease(repo string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return "", fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned %s for %s", resp.Status, url)
	}

	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if rel.TagName == "" {
		return "", fmt.Errorf("empty tag_name from %s", url)
	}
	return rel.TagName, nil
}

// downloadAndReplace fetches the platform tarball for repo@tag, extracts
// binaryName from it, and replaces destPath atomically where possible.
//
// Tarball naming: {project}_{os}_{arch}.tar.gz
// e.g. ralph-ban_darwin_arm64.tar.gz, beads-lite_darwin_arm64.tar.gz
func downloadAndReplace(repo, tag, binaryName, destPath string) error {
	// Derive project name from repo (everything after the "/").
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid repo %q", repo)
	}
	project := parts[1]

	tarball := fmt.Sprintf("%s_%s_%s.tar.gz", project, runtime.GOOS, runtime.GOARCH)
	url := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, tag, tarball)

	// Download to a temp file in the same directory as the destination so that
	// os.Rename is more likely to succeed (same filesystem).
	dir := filepath.Dir(destPath)
	tmp, err := os.CreateTemp(dir, "rb-update-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // cleaned up whether we succeed or fail

	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		tmp.Close()
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		tmp.Close()
		return fmt.Errorf("download %s: status %s", url, resp.Status)
	}

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return fmt.Errorf("write tarball: %w", err)
	}
	tmp.Close()

	// Extract the binary from the tarball using the system tar command.
	extractDir, err := os.MkdirTemp("", "rb-extract-*")
	if err != nil {
		return fmt.Errorf("create extract dir: %w", err)
	}
	defer os.RemoveAll(extractDir)

	cmd := exec.Command("tar", "-xzf", tmpPath, "-C", extractDir, binaryName)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tar extract: %w\n%s", err, out)
	}

	extracted := filepath.Join(extractDir, binaryName)
	if err := os.Chmod(extracted, 0755); err != nil {
		return fmt.Errorf("chmod extracted binary: %w", err)
	}

	// Try atomic rename first; fall back to copy if cross-device.
	if err := os.Rename(extracted, destPath); err != nil {
		if err2 := copyFile(extracted, destPath); err2 != nil {
			return fmt.Errorf("replace binary (rename: %v, copy: %v)", err, err2)
		}
	}

	return nil
}

// copyFile copies src to dst, preserving the dst permissions if it already
// exists so the binary stays executable.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	// Preserve existing permissions if the file exists.
	perm := os.FileMode(0755)
	if fi, err := os.Stat(dst); err == nil {
		perm = fi.Mode()
	}

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
