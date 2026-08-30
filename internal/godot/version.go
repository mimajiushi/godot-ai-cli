package godot

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Version is a parsed Godot version. Godot reports versions like
// "4.7.2.stable.mono.official.abc123" or "4.5.stable.official" — the patch
// segment is optional, and "mono" marks a .NET build.
type Version struct {
	Major int
	Minor int
	Patch int
	Mono  bool
	Raw   string
}

// String renders the canonical dotted form (without status/build suffixes).
func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// versionPattern matches the leading <major>.<minor>[.<patch>] triple of a
// Godot --version line; anything after it is status/build metadata.
var versionPattern = regexp.MustCompile(`^(\d+)\.(\d+)(?:\.(\d+))?(?:\.|$)`)

// ParseVersion parses one Godot version string. It tolerates trailing
// newline and extra tokens (dev builds append hashes and custom names).
func ParseVersion(raw string) (Version, error) {
	line := strings.TrimSpace(raw)
	v := Version{Raw: line, Mono: strings.Contains(line, ".mono")}
	match := versionPattern.FindStringSubmatch(line)
	if match == nil {
		return Version{}, fmt.Errorf("unparseable godot version: %q", line)
	}
	v.Major, _ = strconv.Atoi(match[1])
	v.Minor, _ = strconv.Atoi(match[2])
	if match[3] != "" {
		v.Patch, _ = strconv.Atoi(match[3])
	}
	return v, nil
}

// Version runs `<bin> --version` and parses the first output line.
func VersionFromBinary(path string) (Version, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return Version{}, fmt.Errorf("run %s --version: %w", path, err)
	}
	return ParseVersion(strings.SplitN(string(out), "\n", 2)[0])
}

// CheckCompatibility mirrors the upstream support floor: Godot 4.5+ is
// supported, 4.7+ recommended. It returns a warning for supported-but-old
// or untested-major versions and an error for unsupported ones.
func CheckCompatibility(v Version) (warn string, err error) {
	if v.Major < 4 || (v.Major == 4 && v.Minor < 5) {
		return "", fmt.Errorf("Godot %s is not supported: godot-ai-cli requires Godot 4.5+ (4.7+ recommended)", v.Raw)
	}
	if v.Major >= 5 {
		return fmt.Sprintf("Godot %s is an untested major version: godot-ai-cli is verified against Godot 4.x (4.5+, 4.7+ recommended)", v.Raw), nil
	}
	if v.Major == 4 && v.Minor < 7 {
		return fmt.Sprintf("Godot 4.7+ is recommended (detected %s)", v.Raw), nil
	}
	return "", nil
}
