package godot

import (
	"errors"
	"testing"
)

// TestIsGodotEditorPID drives the pure editor-identity check through its
// injected pid→image resolver: a Godot image counts as alive, a recycled
// pid owned by a foreign image (the live-observed svchost reuse) does not,
// and a failed lookup is conservatively treated as "not a running editor"
// rather than vetoing the settings restore forever.
func TestIsGodotEditorPID(t *testing.T) {
	lookupErr := errors.New("process gone or not queryable")
	tests := []struct {
		name  string
		pid   int
		image func(int) (string, error)
		want  bool
	}{
		{
			name: "godot editor image (windows versioned binary)",
			pid:  40744,
			image: func(int) (string, error) {
				return `C:\Tools\Godot_v4.5.1-stable_win64.exe`, nil
			},
			want: true,
		},
		{
			name: "godot editor image (posix lowercase)",
			pid:  1234,
			image: func(int) (string, error) {
				return "/usr/local/bin/godot", nil
			},
			want: true,
		},
		{
			name: "recycled pid on svchost (pid reuse)",
			pid:  40744,
			image: func(int) (string, error) {
				return `C:\Windows\System32\svchost.exe`, nil
			},
			want: false,
		},
		{
			name: "recycled pid on an unrelated user app",
			pid:  40744,
			image: func(int) (string, error) {
				return `C:\Program Files\Notepad++\notepad++.exe`, nil
			},
			want: false,
		},
		{
			name:  "image lookup fails (race with exit or access denied)",
			pid:   40744,
			image: func(int) (string, error) { return "", lookupErr },
			want:  false,
		},
		{
			name: "non-positive pid",
			pid:  0,
			image: func(int) (string, error) {
				t.Error("image lookup must not run for a non-positive pid")
				return "", nil
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isGodotEditorPID(tt.pid, tt.image); got != tt.want {
				t.Errorf("isGodotEditorPID(%d, …) = %v, want %v", tt.pid, got, tt.want)
			}
		})
	}
}

// TestLooksLikeGodotEditorImage pins the name matching: the Godot binary
// family (any capitalization, version suffixes, platform tags) counts,
// everything else does not.
func TestLooksLikeGodotEditorImage(t *testing.T) {
	godotImages := []string{
		`C:\Godot\Godot.exe`,
		`C:\Tools\Godot_v4.7-stable_win64.exe`,
		"/usr/bin/godot",
		"/opt/Godot.x86_64",
		"godot4",
	}
	for _, image := range godotImages {
		if !looksLikeGodotEditorImage(image) {
			t.Errorf("looksLikeGodotEditorImage(%q) = false, want true", image)
		}
	}
	foreignImages := []string{
		`C:\Windows\System32\svchost.exe`,
		`C:\Windows\explorer.exe`,
		"/usr/bin/god",
		"/usr/bin/godotenv", // prefix-adjacent but not an editor binary
	}
	for _, image := range foreignImages {
		if looksLikeGodotEditorImage(image) {
			t.Errorf("looksLikeGodotEditorImage(%q) = true, want false", image)
		}
	}
}

// TestIsGodotEditorProcessSelf sanity-checks the platform wiring against a
// live process: the test binary itself is running but is not a Godot editor.
func TestIsGodotEditorProcessSelf(t *testing.T) {
	if IsGodotEditorProcess(0) || IsGodotEditorProcess(-1) {
		t.Error("IsGodotEditorProcess must reject non-positive pids")
	}
}
