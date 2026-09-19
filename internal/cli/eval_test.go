package cli

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// assertEvalCodeErr asserts the resolveEvalCode failure code.
func assertEvalCodeErr(t *testing.T, err error, want string) *evalCodeError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s, got nil", want)
	}
	var coded *evalCodeError
	if !errors.As(err, &coded) {
		t.Fatalf("error %v is not an evalCodeError", err)
	}
	if coded.code != want {
		t.Errorf("error code = %s, want %s (%v)", coded.code, want, coded)
	}
	return coded
}

// TestEvalCodeSourceFlagsRegistered: the three CLI-side channels exist on the
// eval leaf (they are declared in the op table, so commands --json and the
// generated catalog pick them up too).
func TestEvalCodeSourceFlagsRegistered(t *testing.T) {
	_, cmd := leafFor(t, "editor", "eval")
	for _, name := range []string{"code", "code-file", "code-stdin", "code-b64"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("editor eval is missing --%s", name)
		}
	}
}

// TestResolveEvalCodePlainPathUntouched: no CLI-side source leaves the
// historical --code path exactly as it was.
func TestResolveEvalCodePlainPathUntouched(t *testing.T) {
	op, cmd := leafFor(t, "editor", "eval")
	if err := resolveEvalCode(cmd, strings.NewReader("ignored")); err != nil {
		t.Fatalf("resolveEvalCode: %v", err)
	}
	// No source at all still fails the shared required-param check.
	if _, err := collectParams(cmd, op); err == nil {
		t.Error("missing --code should still fail the required-param check")
	}
	if err := cmd.Flags().Set("code", "return 1"); err != nil {
		t.Fatal(err)
	}
	if err := resolveEvalCode(cmd, strings.NewReader("ignored")); err != nil {
		t.Fatalf("resolveEvalCode: %v", err)
	}
	params, err := collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	if params["code"] != "return 1" {
		t.Errorf("code = %v", params["code"])
	}
}

// TestResolveEvalCodeFile: a UTF-8 file (BOM tolerated, CRLF and the trailing
// newline preserved) becomes the wire `code` param.
func TestResolveEvalCodeFile(t *testing.T) {
	op, cmd := leafFor(t, "editor", "eval")
	path := filepath.Join(t.TempDir(), "probe.gd")
	want := "return \"abc\".length()\r\n"
	// PowerShell 5.1's `Out-File -Encoding utf8` writes this BOM.
	if err := os.WriteFile(path, []byte("\ufeff"+want), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("code-file", path); err != nil {
		t.Fatal(err)
	}
	if err := resolveEvalCode(cmd, strings.NewReader("ignored")); err != nil {
		t.Fatalf("resolveEvalCode: %v", err)
	}
	params, err := collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	if params["code"] != want {
		t.Errorf("code = %q, want %q (BOM must be stripped, the rest verbatim)", params["code"], want)
	}
}

// TestResolveEvalCodeFileErrors: a missing file and an empty file are input
// errors, never a request with empty code.
func TestResolveEvalCodeFileErrors(t *testing.T) {
	_, cmd := leafFor(t, "editor", "eval")
	if err := cmd.Flags().Set("code-file", filepath.Join(t.TempDir(), "nope.gd")); err != nil {
		t.Fatal(err)
	}
	assertEvalCodeErr(t, resolveEvalCode(cmd, strings.NewReader("")), "EVAL_CODE_SOURCE_ERROR")

	empty := filepath.Join(t.TempDir(), "empty.gd")
	if err := os.WriteFile(empty, []byte("\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, cmd = leafFor(t, "editor", "eval")
	if err := cmd.Flags().Set("code-file", empty); err != nil {
		t.Fatal(err)
	}
	assertEvalCodeErr(t, resolveEvalCode(cmd, strings.NewReader("")), "EVAL_CODE_SOURCE_ERROR")
}

// TestResolveEvalCodeStdin: piped code becomes the wire param.
func TestResolveEvalCodeStdin(t *testing.T) {
	op, cmd := leafFor(t, "editor", "eval")
	if err := cmd.Flags().Set("code-stdin", "true"); err != nil {
		t.Fatal(err)
	}
	want := "return get_tree().get_nodes_in_group(\"bullet\").size()\n"
	if err := resolveEvalCode(cmd, strings.NewReader(want)); err != nil {
		t.Fatalf("resolveEvalCode: %v", err)
	}
	params, err := collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	if params["code"] != want {
		t.Errorf("code = %q, want %q", params["code"], want)
	}
}

// TestResolveEvalCodeStdinTerminalRefused: a char-device stdin means nothing
// was piped in; the command must fail instead of blocking forever. os.DevNull
// is a character device on every supported platform, so the guard is
// exercised for real rather than through a fake.
func TestResolveEvalCodeStdinTerminalRefused(t *testing.T) {
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Skipf("cannot open %s: %v", os.DevNull, err)
	}
	defer devNull.Close()
	if fi, err := devNull.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		t.Skipf("%s is not a character device here", os.DevNull)
	}
	_, cmd := leafFor(t, "editor", "eval")
	if err := cmd.Flags().Set("code-stdin", "true"); err != nil {
		t.Fatal(err)
	}
	coded := assertEvalCodeErr(t, resolveEvalCode(cmd, devNull), "EVAL_CODE_SOURCE_ERROR")
	if !strings.Contains(coded.msg, "--code-file") {
		t.Errorf("the terminal refusal must name the fix, got %q", coded.msg)
	}
}

// TestResolveEvalCodeBase64: the fallback channel accepts the standard and
// URL-safe alphabets, padded or raw, with embedded whitespace.
func TestResolveEvalCodeBase64(t *testing.T) {
	code := "return get_tree().current_scene.get_node(\"Player\").spiral_angle_step"
	raw := base64.StdEncoding.EncodeToString([]byte(code))
	cases := map[string]string{
		"std padded": raw,
		"raw":        strings.TrimRight(raw, "="),
		"url-safe":   base64.URLEncoding.EncodeToString([]byte(code)),
		"wrapped":    raw[:8] + "\n  " + raw[8:],
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			op, cmd := leafFor(t, "editor", "eval")
			if err := cmd.Flags().Set("code-b64", value); err != nil {
				t.Fatal(err)
			}
			if err := resolveEvalCode(cmd, strings.NewReader("")); err != nil {
				t.Fatalf("resolveEvalCode: %v", err)
			}
			params, err := collectParams(cmd, op)
			if err != nil {
				t.Fatal(err)
			}
			if params["code"] != code {
				t.Errorf("code = %q, want %q", params["code"], code)
			}
		})
	}
}

// TestResolveEvalCodeBase64Invalid: garbage and empty values are input errors.
func TestResolveEvalCodeBase64Invalid(t *testing.T) {
	for _, value := range []string{"cmV0dXJuIDE=@@", "not base64 at all!"} {
		_, cmd := leafFor(t, "editor", "eval")
		if err := cmd.Flags().Set("code-b64", value); err != nil {
			t.Fatal(err)
		}
		assertEvalCodeErr(t, resolveEvalCode(cmd, strings.NewReader("")), "EVAL_CODE_SOURCE_ERROR")
	}
	_, cmd := leafFor(t, "editor", "eval")
	if err := cmd.Flags().Set("code-b64", "   "); err != nil {
		t.Fatal(err)
	}
	assertEvalCodeErr(t, resolveEvalCode(cmd, strings.NewReader("")), "EVAL_CODE_SOURCE_ERROR")
}

// TestResolveEvalCodeConflicts: two sources at once is a hard error naming
// both, never a silent priority rule.
func TestResolveEvalCodeConflicts(t *testing.T) {
	cases := []struct {
		name  string
		flags [][2]string
		want  []string
	}{
		{
			name:  "code and code-file",
			flags: [][2]string{{"code", "return 1"}, {"code-file", "whatever.gd"}},
			want:  []string{"--code", "--code-file"},
		},
		{
			name:  "code-file and code-b64",
			flags: [][2]string{{"code-file", "whatever.gd"}, {"code-b64", "cmV0dXJuIDE="}},
			want:  []string{"--code-file", "--code-b64"},
		},
		{
			name:  "code-stdin and code-b64",
			flags: [][2]string{{"code-stdin", "true"}, {"code-b64", "cmV0dXJuIDE="}},
			want:  []string{"--code-stdin", "--code-b64"},
		},
		{
			name:  "params code and code-file",
			flags: [][2]string{{"params", `{"code":"return 1"}`}, {"code-file", "whatever.gd"}},
			want:  []string{"--params code", "--code-file"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, cmd := leafFor(t, "editor", "eval")
			for _, f := range c.flags {
				if err := cmd.Flags().Set(f[0], f[1]); err != nil {
					t.Fatal(err)
				}
			}
			coded := assertEvalCodeErr(t, resolveEvalCode(cmd, strings.NewReader("")), "EVAL_CODE_SOURCE_CONFLICT")
			for _, want := range c.want {
				if !strings.Contains(coded.msg, want) {
					t.Errorf("conflict message %q does not name %s", coded.msg, want)
				}
			}
		})
	}
}

// TestResolveEvalCodeParamsOnlyCode: a --params-supplied code alone still
// works (the base object is not a CLI-side channel).
func TestResolveEvalCodeParamsOnlyCode(t *testing.T) {
	op, cmd := leafFor(t, "editor", "eval")
	if err := cmd.Flags().Set("params", `{"code":"return 7"}`); err != nil {
		t.Fatal(err)
	}
	if err := resolveEvalCode(cmd, strings.NewReader("")); err != nil {
		t.Fatalf("resolveEvalCode: %v", err)
	}
	params, err := collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	if params["code"] != "return 7" {
		t.Errorf("code = %v", params["code"])
	}
}
