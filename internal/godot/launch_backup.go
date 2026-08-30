package godot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Launch with custom ports rewrites the user's GLOBAL EditorSettings
// (godot_ai/http_port, godot_ai/ws_port, managed_server_*). That state is
// shared across projects and with the upstream plugin, so every mutation
// is preceded by a backup capture and `stop` restores it. The backup lives
// at <user cache dir>/godot-ai-cli/launch-backup-<httpPort>.json.
//
// Restore semantics (byte-identical round-trip):
//   - keys present before launch: their original `key = value` line is
//     written back in place of ours (Godot always writes `key = value`
//     with single spaces, so reconstructing the line is byte-exact);
//   - keys absent before launch: the line we appended is removed again;
//   - a settings file that did not exist before launch is deleted again
//     once no other settings remain in it.

// ManagedSettingKeys lists every EditorSettings key launch may overwrite.
var ManagedSettingKeys = []string{
	"godot_ai/http_port",
	"godot_ai/ws_port",
	"godot_ai/managed_server_pid",
	"godot_ai/managed_server_version",
	"godot_ai/managed_server_ws_port",
	"godot_ai/managed_server_ws_token",
}

// BackupValue records one key's pre-launch state. Value is the raw text
// after "key = " (verbatim, quotes included for strings).
type BackupValue struct {
	Present bool   `json:"present"`
	Value   string `json:"value"`
}

// SettingsBackup is the on-disk launch-backup-<httpPort>.json document.
type SettingsBackup struct {
	Keys map[string]BackupValue `json:"keys"`
	// FilePresent records whether the EditorSettings file existed at
	// capture time; restore deletes a file we created from scratch.
	FilePresent        bool   `json:"file_present"`
	Project            string `json:"project"`
	EditorSettingsPath string `json:"editor_settings_path"`
	CreatedAt          string `json:"created_at"`
	// EditorPIDs records every editor process launched while this backup's
	// overrides were active. stop checks their liveness before restoring:
	// a surviving editor would re-save the overridden settings on exit,
	// resurrecting the overrides after the restore.
	EditorPIDs []int `json:"editor_pids,omitempty"`
}

// LaunchBackupPath returns the backup location for one daemon HTTP port.
func LaunchBackupPath(httpPort int) string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "godot-ai-cli", fmt.Sprintf("launch-backup-%d.json", httpPort))
}

// keyValueLine matches `key = <value>` at line start; group 1 is the value.
func keyValueLine(key string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + ` = (.*)$`)
}

// keyWholeLine matches the entire line (including its line ending) so an
// absent-before-launch key can be removed byte-cleanly.
func keyWholeLine(key string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + ` = [^\r\n]*\r?\n?`)
}

// CaptureLaunchBackup records the pre-launch state of every managed key.
// When a backup for this httpPort already exists it is kept untouched —
// overwriting it would capture our own mutated state instead of the
// user's original settings.
func CaptureLaunchBackup(v Version, httpPort int, projectDir string) (created bool, err error) {
	path := LaunchBackupPath(httpPort)
	if _, err := os.Stat(path); err == nil {
		return false, nil
	}

	settingsPath, err := EditorSettingsPath(v)
	if err != nil {
		return false, err
	}
	backup := SettingsBackup{
		Keys:               map[string]BackupValue{},
		Project:            projectDir,
		EditorSettingsPath: settingsPath,
		CreatedAt:          time.Now().UTC().Format(time.RFC3339),
	}
	data, readErr := os.ReadFile(settingsPath)
	backup.FilePresent = readErr == nil
	for _, key := range ManagedSettingKeys {
		if readErr == nil {
			if m := keyValueLine(key).FindStringSubmatch(string(data)); m != nil {
				backup.Keys[key] = BackupValue{Present: true, Value: m[1]}
				continue
			}
		}
		backup.Keys[key] = BackupValue{Present: false}
	}

	payload, err := json.MarshalIndent(backup, "", "  ")
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, payload, 0o600)
}

// RecordBackupEditorPID adds one editor pid to the backup's pid list.
func RecordBackupEditorPID(httpPort, pid int) error {
	return AddBackupEditorPIDs(httpPort, []int{pid})
}

// AddBackupEditorPIDs appends pids to the backup's editor-pid list
// (deduped, non-positive pids ignored). It is a no-op when no backup
// exists — without a backup there are no overrides to protect.
func AddBackupEditorPIDs(httpPort int, pids []int) error {
	if len(pids) == 0 {
		return nil
	}
	return updateBackup(httpPort, func(backup *SettingsBackup) {
		for _, pid := range pids {
			if pid <= 0 {
				continue
			}
			known := false
			for _, existing := range backup.EditorPIDs {
				if existing == pid {
					known = true
					break
				}
			}
			if !known {
				backup.EditorPIDs = append(backup.EditorPIDs, pid)
			}
		}
	})
}

// BackupEditorPIDs returns the backup's recorded editor pids, or nil when
// no backup exists or it is unreadable.
func BackupEditorPIDs(httpPort int) []int {
	data, err := os.ReadFile(LaunchBackupPath(httpPort))
	if err != nil {
		return nil
	}
	var backup SettingsBackup
	if err := json.Unmarshal(data, &backup); err != nil {
		return nil
	}
	return backup.EditorPIDs
}

// updateBackup applies mutate to the stored backup and writes it back.
// A missing backup is a silent no-op.
func updateBackup(httpPort int, mutate func(*SettingsBackup)) error {
	path := LaunchBackupPath(httpPort)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var backup SettingsBackup
	if err := json.Unmarshal(data, &backup); err != nil {
		return fmt.Errorf("parse backup %s: %w", path, err)
	}
	mutate(&backup)
	payload, err := json.MarshalIndent(backup, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, payload, 0o600)
}

// FindOtherLaunchBackup reports whether a launch backup for a DIFFERENT
// daemon HTTP port exists — proof that another launch's settings overrides
// are still active in the shared global EditorSettings file. Launch refuses
// to stack a second override session on top of it.
func FindOtherLaunchBackup(exceptPort int) (port int, found bool) {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	entries, err := os.ReadDir(filepath.Join(dir, "godot-ai-cli"))
	if err != nil {
		return 0, false
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "launch-backup-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, "launch-backup-"), ".json"))
		if err != nil || n == exceptPort {
			continue
		}
		return n, true
	}
	return 0, false
}

// RestoreLaunchBackup puts every managed key back to its pre-launch state
// and deletes the backup file. It is a silent no-op (false, nil) when no
// backup exists. The backup file is only deleted after a successful
// restore, so a failure leaves the state recoverable by hand.
func RestoreLaunchBackup(httpPort int) (restored bool, err error) {
	path := LaunchBackupPath(httpPort)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var backup SettingsBackup
	if err := json.Unmarshal(data, &backup); err != nil {
		return false, fmt.Errorf("parse backup %s: %w", path, err)
	}

	if err := restoreSettings(&backup); err != nil {
		return false, err
	}
	if err := os.Remove(path); err != nil {
		return false, fmt.Errorf("remove backup %s: %w", path, err)
	}
	return true, nil
}

// restoreSettings applies one backup to its EditorSettings file.
func restoreSettings(backup *SettingsBackup) error {
	content := ""
	data, err := os.ReadFile(backup.EditorSettingsPath)
	switch {
	case err == nil:
		content = string(data)
	case os.IsNotExist(err) && !backup.FilePresent:
		return nil // file was absent before launch and still is: done
	default:
		return fmt.Errorf("read %s: %w", backup.EditorSettingsPath, err)
	}

	nl := "\n"
	if strings.Contains(content, "\r\n") {
		nl = "\r\n"
	}

	// Deterministic key order keeps the restore reproducible.
	for _, key := range ManagedSettingKeys {
		value, ok := backup.Keys[key]
		if !ok {
			continue
		}
		if value.Present {
			content = replaceOrAppend(content, keyValueLine(key), key+" = "+value.Value, nl)
		} else {
			content = keyWholeLine(key).ReplaceAllString(content, "")
		}
	}

	if !backup.FilePresent {
		// The file did not exist before launch: once no settings remain,
		// delete it so the post-restore state matches byte-for-byte.
		if !settingsPropertyLine.MatchString(content) {
			if err := os.Remove(backup.EditorSettingsPath); err != nil && !os.IsNotExist(err) {
				return err
			}
			return nil
		}
		// The editor added its own settings meanwhile: keep the file.
	}
	return os.WriteFile(backup.EditorSettingsPath, []byte(content), 0o644)
}

// settingsPropertyLine matches an EditorSettings property assignment
// (`some/key = value`), distinguishing content from bare section headers.
var settingsPropertyLine = regexp.MustCompile(`(?m)^[^\s\[][^\n]* = `)
