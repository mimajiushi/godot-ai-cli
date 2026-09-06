package godot

import (
	"fmt"
	"os"
	"os/exec"
)

// LaunchOptions configures one detached editor launch.
type LaunchOptions struct {
	// Binary is the Godot executable path (from Find).
	Binary string
	// ProjectDir is the project root containing project.godot.
	ProjectDir string
	// Headless adds --headless and exports GODOT_AI_ALLOW_HEADLESS=1 so the
	// plugin accepts a windowless editor.
	Headless bool
	// ExtraArgs are appended verbatim after the built-in flags.
	ExtraArgs []string
}

// editorEnv builds the editor child-process environment: the inherited
// environment plus GODOT_AI_CLI_LAUNCHED=1 so the plugin can tag its
// handshake as CLI-spawned (launched_by="cli"), plus the headless opt-in
// when requested.
func editorEnv(headless bool) []string {
	env := append(os.Environ(), "GODOT_AI_CLI_LAUNCHED=1")
	if headless {
		env = append(env, "GODOT_AI_ALLOW_HEADLESS=1")
	}
	return env
}

// LaunchEditor starts the Godot editor DETACHED from this process: the
// editor survives CLI exit and terminal close. It returns the child pid.
func LaunchEditor(opts LaunchOptions) (pid int, err error) {
	if opts.Binary == "" || opts.ProjectDir == "" {
		return 0, fmt.Errorf("LaunchEditor: Binary and ProjectDir are required")
	}
	args := []string{"--editor", "--path", opts.ProjectDir}
	if opts.Headless {
		args = append(args, "--headless")
	}
	args = append(args, opts.ExtraArgs...)

	cmd := exec.Command(opts.Binary, args...)
	cmd.Env = editorEnv(opts.Headless)
	// No stdio wiring: inherited handles would keep the CLI's console (and
	// any pipe reader) alive for the editor's whole lifetime.
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = detachedSysProcAttr()

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("launch editor %s: %w", opts.Binary, err)
	}
	pid = cmd.Process.Pid
	// Release drops our handle without waiting — the editor is meant to
	// outlive us.
	_ = cmd.Process.Release()
	return pid, nil
}
