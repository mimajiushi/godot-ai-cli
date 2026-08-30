// Package godot locates the Godot editor binary, parses its version, and
// launches editor processes detached from the CLI.
package godot

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
)

// Find resolves the Godot binary in strict precedence order:
//
//  1. explicit (--godot flag) — must exist, error otherwise
//  2. GODOT_BIN environment variable — must exist when set
//  3. PATH lookup (godot / godot.exe)
//  4. conventional per-OS install locations
//
// It returns an error naming every attempted source when nothing matches.
func Find(explicit string) (string, error) {
	if explicit != "" {
		if fileExists(explicit) {
			return explicit, nil
		}
		return "", fmt.Errorf("godot binary from --godot does not exist: %s", explicit)
	}
	if env := os.Getenv("GODOT_BIN"); env != "" {
		if fileExists(env) {
			return env, nil
		}
		return "", fmt.Errorf("godot binary from GODOT_BIN does not exist: %s", env)
	}
	if path, err := exec.LookPath("godot"); err == nil {
		return path, nil
	}
	if locations := conventionalLocations(); len(locations) > 0 {
		return locations[0], nil
	}
	return "", fmt.Errorf("godot binary not found: pass --godot, set GODOT_BIN, or add godot to PATH")
}

// Candidates returns every Godot binary that exists on this machine, in
// the precedence order Find uses (GODOT_BIN, PATH, conventional
// locations), deduplicated. It backs `godot-ai-cli godot detect`.
func Candidates() []string {
	var out []string
	seen := map[string]bool{}
	add := func(path string) {
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		if !seen[abs] {
			seen[abs] = true
			out = append(out, path)
		}
	}
	if env := os.Getenv("GODOT_BIN"); env != "" && fileExists(env) {
		add(env)
	}
	if path, err := exec.LookPath("godot"); err == nil {
		add(path)
	}
	for _, loc := range conventionalLocations() {
		add(loc)
	}
	return out
}

// conventionalLocations expands the well-known install locations for the
// current OS and returns those that exist. Only cross-machine conventions
// are listed — never machine-specific paths.
func conventionalLocations() []string {
	var patterns []string
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData != "" {
			// Both flat installs and versioned subdirectories are common.
			patterns = append(patterns,
				filepath.Join(localAppData, `Programs\Godot\*.exe`),
				filepath.Join(localAppData, `Programs\Godot\*\*.exe`),
			)
		}
		patterns = append(patterns,
			`C:\Program Files\Godot\*.exe`,
			`C:\Program Files\Godot\*\*.exe`,
		)
		if home != "" {
			patterns = append(patterns, filepath.Join(home, `scoop\shims\godot.exe`))
		}
		for _, root := range []string{os.Getenv("ProgramFiles(x86)"), `C:\Program Files (x86)`} {
			if root != "" {
				patterns = append(patterns,
					filepath.Join(root, `Steam\steamapps\common\Godot Engine\*.exe`))
			}
		}
	case "darwin":
		patterns = append(patterns, "/Applications/Godot.app/Contents/MacOS/Godot")
	default: // linux and other unix
		patterns = append(patterns,
			"/usr/local/bin/godot",
			"/usr/bin/godot",
		)
		if home != "" {
			patterns = append(patterns, filepath.Join(home, ".local/bin/godot"))
		}
	}

	var out []string
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		// Sort glob hits so the resolution order is deterministic.
		sort.Strings(matches)
		for _, m := range matches {
			if fileExists(m) {
				out = append(out, m)
			}
		}
	}
	return out
}

// fileExists reports whether path names an existing regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
