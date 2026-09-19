package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestPreferConsoleVariant：Windows 上存在 <name>_console.exe 时优先，
// 不存在或非 Windows 保持原样。
func TestPreferConsoleVariant(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("console variant is a Windows concept")
	}
	dir := t.TempDir()
	gui := filepath.Join(dir, "Godot_v4.7.2_win64.exe")
	console := filepath.Join(dir, "Godot_v4.7.2_win64_console.exe")
	if err := os.WriteFile(gui, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := preferConsoleVariant(gui); got != gui {
		t.Errorf("no console sibling: got %q, want %q", got, gui)
	}
	if err := os.WriteFile(console, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := preferConsoleVariant(gui); got != console {
		t.Errorf("console sibling exists: got %q, want %q", got, console)
	}
}

// TestScriptRunValidation：非工程目录报 INVALID_PROJECT；脚本必报缺失由 cobra 拦。
func TestScriptRunValidation(t *testing.T) {
	dir := t.TempDir() // 没有 project.godot
	cmd := newScriptRunCommand()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"--project", dir, "--script", "res://probe.gd"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("non-project dir succeeded, want INVALID_PROJECT")
	}
	if !strings.Contains(buf.String(), "INVALID_PROJECT") {
		t.Errorf("want INVALID_PROJECT envelope, got %s", buf.String())
	}
}
