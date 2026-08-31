package godot_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mimajiushi/godot-ai-cli/internal/godot"
)

// TestRestoreDollarSignValueByteIdentical pins the regex-replacement trap:
// a backed-up value containing "$1" must be written back LITERALLY on
// restore, not expanded as a group reference.
func TestRestoreDollarSignValueByteIdentical(t *testing.T) {
	root := withEditorConfig(t)
	withCacheDir(t, root)
	path := settingsFile(t, root, testVersion47)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original := "[gd_resource type=\"EditorSettings\" format=3]\n\n[resource]\n" +
		"godot_ai/managed_server_version = \"3.2.4\"\n" +
		"godot_ai/managed_server_ws_token = \"ab$1cd\"\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := godot.CaptureLaunchBackup(testVersion47, 18099, "C:/proj"); err != nil {
		t.Fatal(err)
	}
	// Mutate: rewrites version/pid/ws_port and clears the token.
	if _, err := godot.SetPluginManagedServer(testVersion47, 999, 19599, "3.2.4"); err != nil {
		t.Fatal(err)
	}
	if _, err := godot.RestoreLaunchBackup(18099); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Errorf("restore not byte-identical ($ injection?):\n--- original ---\n%q\n--- restored ---\n%q", original, after)
	}
}

// TestBackupEditorPIDsRoundTrip covers Record/Add/BackupEditorPIDs,
// including dedup and the no-backup no-op.
func TestBackupEditorPIDsRoundTrip(t *testing.T) {
	root := withEditorConfig(t)
	withCacheDir(t, root)

	if _, err := godot.CaptureLaunchBackup(testVersion47, 18099, "C:/proj"); err != nil {
		t.Fatal(err)
	}
	if err := godot.RecordBackupEditorPID(18099, 1234); err != nil {
		t.Fatal(err)
	}
	if err := godot.AddBackupEditorPIDs(18099, []int{1234, 5678, -1}); err != nil {
		t.Fatal(err)
	}
	got := godot.BackupEditorPIDs(18099)
	if len(got) != 2 || got[0] != 1234 || got[1] != 5678 {
		t.Errorf("BackupEditorPIDs = %v, want [1234 5678] (deduped, non-positive dropped)", got)
	}

	// No backup for this port: silent no-op, no error.
	if err := godot.AddBackupEditorPIDs(28099, []int{1}); err != nil {
		t.Errorf("AddBackupEditorPIDs without backup: %v", err)
	}
	if got := godot.BackupEditorPIDs(28099); got != nil {
		t.Errorf("BackupEditorPIDs without backup = %v, want nil", got)
	}
}

// TestFindOtherLaunchBackup pins the cross-launch guard: a backup for a
// different port is reported, the caller's own port is not.
func TestFindOtherLaunchBackup(t *testing.T) {
	root := withEditorConfig(t)
	withCacheDir(t, root)

	if _, err := godot.CaptureLaunchBackup(testVersion47, 18099, "C:/proj"); err != nil {
		t.Fatal(err)
	}

	port, found := godot.FindOtherLaunchBackup(19000)
	if !found || port != 18099 {
		t.Errorf("FindOtherLaunchBackup(19000) = %d, %v, want 18099, true", port, found)
	}
	if port, found := godot.FindOtherLaunchBackup(18099); found {
		t.Errorf("FindOtherLaunchBackup(18099) = %d, true, want no hit for the own port", port)
	}
}

// TestAcquireLaunchLock exercises the basic acquire/release cycle.
// (Blocking behavior between processes is covered by the OS primitives;
// re-acquiring within one process is not portable to test.)
func TestAcquireLaunchLock(t *testing.T) {
	withCacheDir(t)

	unlock, err := godot.AcquireLaunchLock()
	if err != nil {
		t.Fatalf("AcquireLaunchLock: %v", err)
	}
	unlock()

	// A fresh acquire after release must succeed again.
	unlock2, err := godot.AcquireLaunchLock()
	if err != nil {
		t.Fatalf("second AcquireLaunchLock: %v", err)
	}
	unlock2()
}

// TestIsProcessRunning sanity-checks the liveness probe: our own process
// is alive, non-positive pids are never "running".
func TestIsProcessRunning(t *testing.T) {
	if !godot.IsProcessRunning(os.Getpid()) {
		t.Error("IsProcessRunning(own pid) = false, want true")
	}
	if godot.IsProcessRunning(0) || godot.IsProcessRunning(-5) {
		t.Error("IsProcessRunning must reject non-positive pids")
	}
}
