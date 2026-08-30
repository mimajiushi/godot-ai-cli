package godot

import (
	"fmt"
	"os"
	"path/filepath"
)

// The launch lock serializes `launch` and `stop` across processes: both
// commands read, mutate, and restore the user's GLOBAL EditorSettings, so
// overlapping runs would race (e.g. a restore landing between another
// launch's backup capture and its mutation). The lock file lives at
// <user cache dir>/godot-ai-cli/launch.lock and the lock is held for the
// whole command run. Locking is blocking on purpose: a second command
// waits for the first to finish instead of failing flakily.

// AcquireLaunchLock takes the cross-process launch lock and returns the
// unlock function. The lock is released automatically if the process dies
// (the OS closes the handle), so a crashed CLI never wedges later runs.
func AcquireLaunchLock() (unlock func(), err error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	path := filepath.Join(dir, "godot-ai-cli", "launch.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open launch lock %s: %w", path, err)
	}
	if err := lockFile(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("acquire launch lock %s: %w", path, err)
	}
	return func() {
		_ = unlockFile(f)
		_ = f.Close()
	}, nil
}
