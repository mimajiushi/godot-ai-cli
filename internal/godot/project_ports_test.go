package godot_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mimajiushi/godot-ai-cli/internal/godot"
)

// TestWriteAndReadProjectPorts pins the contract file: launch writes
// <project>/.godot/godot_ai_ports.json (creating .godot when missing) with
// both ports, and the reader parses it back.
func TestWriteAndReadProjectPorts(t *testing.T) {
	dir := t.TempDir()

	if err := godot.WriteProjectPorts(dir, 18010, 19510); err != nil {
		t.Fatalf("WriteProjectPorts: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".godot", "godot_ai_ports.json"))
	if err != nil {
		t.Fatalf("port file not at the contract path: %v", err)
	}
	if string(data) != "{\"http_port\":18010,\"ws_port\":19510}\n" {
		t.Errorf("port file content = %q", data)
	}

	ports, ok := godot.ReadProjectPorts(dir)
	if !ok || ports.HTTPPort != 18010 || ports.WSPort != 19510 {
		t.Errorf("ReadProjectPorts = %+v, %v", ports, ok)
	}
	if !godot.HasProjectPorts(dir) {
		t.Error("HasProjectPorts = false after write")
	}

	// Default ports are written too (deterministic resolution).
	if err := godot.WriteProjectPorts(dir, 8000, 9500); err != nil {
		t.Fatalf("WriteProjectPorts defaults: %v", err)
	}
	ports, ok = godot.ReadProjectPorts(dir)
	if !ok || ports.HTTPPort != 8000 || ports.WSPort != 9500 {
		t.Errorf("after overwrite ReadProjectPorts = %+v, %v", ports, ok)
	}
}

// TestReadProjectPortsToleratesMissingAndCorrupt: absent and garbage files
// are a silent !ok, never an error.
func TestReadProjectPortsToleratesMissingAndCorrupt(t *testing.T) {
	dir := t.TempDir()
	if _, ok := godot.ReadProjectPorts(dir); ok {
		t.Error("missing file reported ok")
	}
	if godot.HasProjectPorts(dir) {
		t.Error("HasProjectPorts = true without a file")
	}

	path := godot.ProjectPortsPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := godot.ReadProjectPorts(dir); ok {
		t.Error("corrupt file reported ok")
	}
	if godot.HasProjectPorts(dir) {
		t.Error("HasProjectPorts = true for a corrupt file")
	}
}

// TestRemoveProjectPorts pins the deletion guard: only a file still
// pointing at the daemon being stopped is removed; a repinned file (newer
// launch on another daemon) survives; a missing file is a silent no-op.
func TestRemoveProjectPorts(t *testing.T) {
	dir := t.TempDir()

	// Missing file: no-op, no error.
	if removed, err := godot.RemoveProjectPorts(dir, 18010); removed || err != nil {
		t.Errorf("missing file: removed=%v err=%v", removed, err)
	}

	if err := godot.WriteProjectPorts(dir, 18010, 19510); err != nil {
		t.Fatal(err)
	}
	// A stop for a DIFFERENT daemon must not delete the pin.
	if removed, err := godot.RemoveProjectPorts(dir, 18020); removed || err != nil {
		t.Errorf("foreign port: removed=%v err=%v", removed, err)
	}
	if !godot.HasProjectPorts(dir) {
		t.Error("foreign-port stop deleted the pin")
	}
	// The matching stop deletes it.
	if removed, err := godot.RemoveProjectPorts(dir, 18010); !removed || err != nil {
		t.Errorf("matching port: removed=%v err=%v", removed, err)
	}
	if godot.HasProjectPorts(dir) {
		t.Error("pin still present after matching stop")
	}

	// Corrupt file: refused, with a manual-recovery error.
	path := godot.ProjectPortsPath(dir)
	if err := os.WriteFile(path, []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	if removed, err := godot.RemoveProjectPorts(dir, 18010); removed || err == nil {
		t.Errorf("corrupt file: removed=%v err=%v, want a refusal error", removed, err)
	}
}
