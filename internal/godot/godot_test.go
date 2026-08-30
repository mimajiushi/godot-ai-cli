package godot_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mimajiushi/godot-ai-cli/internal/godot"
)

// TestParseVersion covers the version shapes Godot actually emits:
// with/without patch, mono marker, rc status, build hashes, dev builds.
func TestParseVersion(t *testing.T) {
	cases := []struct {
		raw                 string
		major, minor, patch int
		mono                bool
		wantErr             bool
	}{
		{"4.7.2.stable.mono.official.abc123", 4, 7, 2, true, false},
		{"4.5.stable.official", 4, 5, 0, false, false},
		{"4.5.1.stable.official", 4, 5, 1, false, false},
		{"4.7.rc1.official", 4, 7, 0, false, false},
		{"4.7.rc2.mono.official", 4, 7, 0, true, false},
		{"4.8.dev4.official.3f2a1b0", 4, 8, 0, false, false},
		{"4.6.stable.custom_build", 4, 6, 0, false, false},
		{"4.7.stable.official\n", 4, 7, 0, false, false},
		{"not-a-version", 0, 0, 0, false, true},
		{"", 0, 0, 0, false, true},
	}
	for _, c := range cases {
		t.Run(c.raw, func(t *testing.T) {
			v, err := godot.ParseVersion(c.raw)
			if c.wantErr {
				if err == nil {
					t.Fatalf("ParseVersion(%q) = %+v, want error", c.raw, v)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseVersion(%q): %v", c.raw, err)
			}
			if v.Major != c.major || v.Minor != c.minor || v.Patch != c.patch || v.Mono != c.mono {
				t.Errorf("ParseVersion(%q) = %+v", c.raw, v)
			}
			if v.Raw == "" {
				t.Error("Raw must keep the original string")
			}
		})
	}
}

// TestCheckCompatibility mirrors the upstream support floor matrix.
func TestCheckCompatibility(t *testing.T) {
	cases := []struct {
		version  string
		wantErr  bool
		wantWarn bool
	}{
		{"3.6.stable.official", true, false},
		{"4.3.stable.official", true, false},
		{"4.4.1.stable.official", true, false},
		{"4.5.stable.official", false, true},
		{"4.6.2.stable.mono.official", false, true},
		{"4.7.stable.official", false, false},
		{"4.7.2.stable.mono.official.abc123", false, false},
		{"4.8.dev4.official", false, false},
		{"5.0.stable.official", false, true},
	}
	for _, c := range cases {
		t.Run(c.version, func(t *testing.T) {
			v, err := godot.ParseVersion(c.version)
			if err != nil {
				t.Fatalf("ParseVersion: %v", err)
			}
			warn, err := godot.CheckCompatibility(v)
			if (err != nil) != c.wantErr {
				t.Errorf("err = %v, wantErr %v", err, c.wantErr)
			}
			if (warn != "") != c.wantWarn {
				t.Errorf("warn = %q, wantWarn %v", warn, c.wantWarn)
			}
			if c.wantErr && err != nil && !strings.Contains(err.Error(), "4.5+") {
				t.Errorf("unsupported error should name the supported range: %v", err)
			}
		})
	}
}

// fakeBinary creates an empty executable-looking file in a temp dir.
func fakeBinary(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestFindPrecedence checks flag > GODOT_BIN > PATH resolution using only
// temp dirs, so the result never depends on the host machine.
func TestFindPrecedence(t *testing.T) {
	flagBin := fakeBinary(t, t.TempDir(), exeName("godot-flag"))
	envBin := fakeBinary(t, t.TempDir(), exeName("godot-env"))
	pathDir := t.TempDir()
	pathBin := fakeBinary(t, pathDir, exeName("godot"))

	t.Run("explicit flag wins over env and PATH", func(t *testing.T) {
		t.Setenv("GODOT_BIN", envBin)
		t.Setenv("PATH", pathDir)
		got, err := godot.Find(flagBin)
		if err != nil {
			t.Fatal(err)
		}
		if got != flagBin {
			t.Errorf("Find(flag) = %q, want %q", got, flagBin)
		}
	})

	t.Run("missing explicit flag errors", func(t *testing.T) {
		if _, err := godot.Find(filepath.Join(t.TempDir(), "nope")); err == nil {
			t.Error("expected error for missing explicit binary")
		}
	})

	t.Run("env wins over PATH", func(t *testing.T) {
		t.Setenv("GODOT_BIN", envBin)
		t.Setenv("PATH", pathDir)
		got, err := godot.Find("")
		if err != nil {
			t.Fatal(err)
		}
		if got != envBin {
			t.Errorf("Find() = %q, want env %q", got, envBin)
		}
	})

	t.Run("invalid env errors instead of falling through", func(t *testing.T) {
		t.Setenv("GODOT_BIN", filepath.Join(t.TempDir(), "nope"))
		if _, err := godot.Find(""); err == nil {
			t.Error("expected error for invalid GODOT_BIN")
		}
	})

	t.Run("PATH used when flag and env are absent", func(t *testing.T) {
		t.Setenv("GODOT_BIN", "")
		t.Setenv("PATH", pathDir)
		got, err := godot.Find("")
		if err != nil {
			t.Fatal(err)
		}
		// LookPath may return the path as given; compare resolved forms.
		gotAbs, _ := filepath.Abs(got)
		wantAbs, _ := filepath.Abs(pathBin)
		if gotAbs != wantAbs {
			t.Errorf("Find() = %q, want PATH hit %q", got, pathBin)
		}
	})
}

// TestCandidatesIncludesPathHit ensures detect sees the PATH binary.
func TestCandidatesIncludesPathHit(t *testing.T) {
	pathDir := t.TempDir()
	pathBin := fakeBinary(t, pathDir, exeName("godot"))
	t.Setenv("GODOT_BIN", "")
	t.Setenv("PATH", pathDir)

	wantAbs, _ := filepath.Abs(pathBin)
	for _, c := range godot.Candidates() {
		abs, _ := filepath.Abs(c)
		if abs == wantAbs {
			return
		}
	}
	t.Errorf("Candidates() = %v, missing PATH binary %q", godot.Candidates(), pathBin)
}

// exeName appends .exe on Windows so LookPath finds the fake binary.
func exeName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}
