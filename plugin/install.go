package plugin

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// addonsRelPath is the plugin's location inside a Godot project, relative
// to the project root. Slash-separated; converted per-OS at use.
const addonsRelPath = "addons/godot_ai"

// InstallResult reports what one install/upgrade did.
type InstallResult struct {
	// Installed is true when files were (re)written by this call.
	Installed bool
	// Upgraded is true when an existing addon carried a different version.
	Upgraded bool
	// Version is the version now on disk.
	Version string
	// PreviousVersion is the version found before the call ("" if none).
	PreviousVersion string
	// Path is the absolute addon directory.
	Path string
	// Enabled is true when project.godot lists godot_ai as enabled.
	Enabled bool
}

// addonDir returns the addon directory for a project.
func addonDir(projectDir string) string {
	return filepath.Join(projectDir, "addons", "godot_ai")
}

// Install extracts the embedded godot_ai tree into
// <projectDir>/addons/godot_ai. It overwrites file contents but NEVER
// deletes the target directory itself (it may be a junction/symlink in dev
// setups) and never deletes extra user files inside it.
//
// Files are written in installPlan order — plugin.cfg LAST. Invariant:
// InstalledVersion keys off plugin.cfg, so a kill mid-extract must never
// leave a version-matching descriptor without the full file tree behind it.
func Install(projectDir string) (InstallResult, error) {
	previous, _ := InstalledVersion(projectDir)
	dest := addonDir(projectDir)

	plan, err := installPlan()
	if err != nil {
		return InstallResult{}, fmt.Errorf("install plugin into %s: %w", dest, err)
	}
	for _, path := range plan {
		// embed paths are slash-separated; make the relative part native.
		rel := strings.TrimPrefix(path, "godot_ai/")
		target := filepath.Join(dest, filepath.FromSlash(rel))
		data, err := FS.ReadFile(path)
		if err != nil {
			return InstallResult{}, fmt.Errorf("install plugin into %s: %w", dest, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return InstallResult{}, fmt.Errorf("install plugin into %s: %w", dest, err)
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return InstallResult{}, fmt.Errorf("install plugin into %s: %w", dest, err)
		}
	}

	version := PluginVersion()
	return InstallResult{
		Installed:       true,
		Upgraded:        previous != "" && previous != version,
		Version:         version,
		PreviousVersion: previous,
		Path:            dest,
	}, nil
}

// installPlan lists every embedded plugin file in write order with
// plugin.cfg LAST (see Install for the invariant).
func installPlan() ([]string, error) {
	var files, descriptors []string
	err := fs.WalkDir(FS, "godot_ai", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if strings.HasSuffix(path, "/plugin.cfg") {
			descriptors = append(descriptors, path)
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return append(files, descriptors...), nil
}

// InstalledVersion reads the version of the addon currently installed in
// the project. It returns "" (with the read error) when not installed.
func InstalledVersion(projectDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(addonDir(projectDir), "plugin.cfg"))
	if err != nil {
		return "", err
	}
	if v := ParseVersion(data); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("no version line in %s", filepath.Join(addonDir(projectDir), "plugin.cfg"))
}

// enabledArrayPattern matches the enabled=PackedStringArray(...) line Godot
// writes inside [editor_plugins] (single-line form).
var enabledArrayPattern = regexp.MustCompile(`enabled=PackedStringArray\(([^)]*)\)`)

// Enable ensures project.godot enables the godot_ai editor plugin. It
// preserves all other content byte-for-byte and reports whether the file
// changed.
func Enable(projectDir string) (changed bool, err error) {
	path := filepath.Join(projectDir, "project.godot")
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	content := string(data)

	// Match the file's line-ending style for any lines we add.
	nl := "\n"
	if strings.Contains(content, "\r\n") {
		nl = "\r\n"
	}

	header := "[editor_plugins]"
	headerIdx := strings.Index(content, header)
	if headerIdx < 0 {
		// No section yet: append one at the end, Godot-style (blank line
		// between the header and its keys, trailing newline).
		var b strings.Builder
		b.WriteString(content)
		if len(content) > 0 && !strings.HasSuffix(content, "\n") {
			b.WriteString(nl)
		}
		b.WriteString(header + nl + nl + `enabled=PackedStringArray("godot_ai")` + nl)
		return true, writeProjectGodot(path, b.String())
	}

	// Section exists: it ends at the next section header or EOF.
	sectionStart := headerIdx + len(header)
	sectionEnd := len(content)
	if next := strings.Index(content[sectionStart:], "\n["); next >= 0 {
		sectionEnd = sectionStart + next + 1
	}
	section := content[headerIdx:sectionEnd]

	if match := enabledArrayPattern.FindStringSubmatch(section); match != nil {
		if strings.Contains(match[1], `"godot_ai"`) {
			return false, nil // already enabled
		}
		// Append godot_ai to the existing array, keeping the other entries.
		args := strings.TrimSpace(match[1])
		if args != "" {
			args += ", "
		}
		args += `"godot_ai"`
		newSection := strings.Replace(section, match[0],
			"enabled=PackedStringArray("+args+")", 1)
		newContent := content[:headerIdx] + newSection + content[sectionEnd:]
		return true, writeProjectGodot(path, newContent)
	}

	// Section without an enabled key: add one right after the header line.
	headerLineEnd := strings.Index(content[headerIdx:], "\n")
	if headerLineEnd < 0 {
		headerLineEnd = len(content) - headerIdx
	}
	insertAt := headerIdx + headerLineEnd + 1
	newContent := content[:insertAt] + nl + `enabled=PackedStringArray("godot_ai")` + nl + content[insertAt:]
	return true, writeProjectGodot(path, newContent)
}

// writeProjectGodot atomically replaces project.godot: write a temp file
// in the same directory, then rename over the original, so a crash
// mid-write never leaves a truncated project file. The original file's
// permission bits are preserved.
func writeProjectGodot(path, content string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".project.godot-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Leftover cleanup on any failure path; a no-op after a successful
	// rename (the temp name no longer exists).
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(info.Mode()); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// EnsureInstalled installs or upgrades the plugin when missing or stale,
// then enables it in project.godot.
func EnsureInstalled(projectDir string) (InstallResult, error) {
	previous, err := InstalledVersion(projectDir)
	var result InstallResult
	switch {
	case err != nil:
		// Not installed (or unreadable plugin.cfg): fresh install.
		result, err = Install(projectDir)
	case previous != PluginVersion():
		result, err = Install(projectDir) // version mismatch: upgrade
	default:
		result = InstallResult{
			Installed:       false,
			Upgraded:        false,
			Version:         PluginVersion(),
			PreviousVersion: previous,
			Path:            addonDir(projectDir),
		}
	}
	if err != nil {
		return InstallResult{}, err
	}
	enabled, err := Enable(projectDir)
	if err != nil {
		return result, err
	}
	// enabled==true means we just changed the file; either way the plugin
	// is enabled after this call.
	result.Enabled = true
	_ = enabled
	return result, nil
}
