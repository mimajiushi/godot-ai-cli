// editor eval 的 CLI 侧代码输入通道：--code-file / --code-stdin / --code-b64
// 让代码完全绕开 shell 的引号规则（Windows PowerShell 5.1 会把原生程序参数里
// 的内嵌双引号吞掉，插件只报一个语法错误码）。三条通道最终都落到同一个 wire
// 参数 `code` 上，插件侧协议不变。
package cli

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mimajiushi/godot-ai-cli/internal/ops"
)

// evalCodeSources lists the mutually exclusive code sources, in the order
// conflict messages name them. "--params code" is a fourth, implicit source
// (the base-params object) and is checked separately.
var evalCodeSources = []string{"code", "code-file", "code-stdin", "code-b64"}

// evalCodeError carries the protocol error code for an unusable code source
// so runEval can turn it into an error envelope verbatim.
type evalCodeError struct {
	code string
	msg  string
}

func (e *evalCodeError) Error() string { return e.msg }

// runEval is the editor eval entry point: resolve the code source into the
// `code` flag first (keeping the documented "--params is base, explicit flags
// win" precedence), then take the shared collectParams + executeOp path so
// every eval keeps the same timeout, session and write-gate semantics.
func runEval(cmd *cobra.Command, op ops.OpSpec) error {
	if err := resolveEvalCode(cmd, os.Stdin); err != nil {
		var coded *evalCodeError
		if errors.As(err, &coded) {
			return jsonError(cmd, coded.code, err.Error(), nil)
		}
		return jsonError(cmd, "EVAL_CODE_SOURCE_ERROR", err.Error(), nil)
	}
	params, err := collectParams(cmd, op)
	if err != nil {
		return jsonError(cmd, "INVALID_PARAMS", err.Error(), nil)
	}
	return executeOp(cmd, op.PluginCommand, params, op.Timeout, op.Write)
}

// resolveEvalCode fills the `code` flag from exactly ONE code source. Two
// sources at once are a hard error rather than a silent priority rule: the
// whole point of these channels is that the code the plugin compiles is the
// code the caller meant. stdin is injected so tests never touch the real one.
func resolveEvalCode(cmd *cobra.Command, stdin io.Reader) error {
	used := make([]string, 0, len(evalCodeSources))
	for _, name := range evalCodeSources {
		if f := cmd.Flags().Lookup(name); f != nil && f.Changed {
			used = append(used, "--"+name)
		}
	}
	// --params carrying a code key counts as a source too: silently letting
	// --code-file override it would hide which snippet actually ran.
	if raw := flagString(cmd, "params"); raw != "" {
		var base map[string]any
		if json.Unmarshal([]byte(raw), &base) == nil {
			if _, ok := base["code"]; ok {
				used = append(used, "--params code")
			}
		}
	}
	if len(used) > 1 {
		return &evalCodeError{"EVAL_CODE_SOURCE_CONFLICT",
			fmt.Sprintf("%s are mutually exclusive — pass exactly one code source",
				strings.Join(used, " and "))}
	}
	switch {
	case len(used) == 0 || used[0] == "--code" || used[0] == "--params code":
		return nil // the plain --code / --params path is untouched
	}

	var code string
	switch used[0] {
	case "--code-file":
		path := flagString(cmd, "code-file")
		data, err := os.ReadFile(path)
		if err != nil {
			return &evalCodeError{"EVAL_CODE_SOURCE_ERROR", fmt.Sprintf("--code-file: %v", err)}
		}
		code = stripUTF8BOM(string(data))
	case "--code-stdin":
		// A terminal stdin means nothing was piped in; reading it would hang
		// forever, so fail with the fix instead.
		if f, ok := stdin.(*os.File); ok {
			if fi, err := f.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
				return &evalCodeError{"EVAL_CODE_SOURCE_ERROR",
					"--code-stdin was given but stdin is a terminal — pipe the code in (`echo 'return 1' | godot-ai-cli editor eval --code-stdin`) or use --code-file"}
			}
		}
		data, err := io.ReadAll(stdin)
		if err != nil {
			return &evalCodeError{"EVAL_CODE_SOURCE_ERROR", fmt.Sprintf("--code-stdin: %v", err)}
		}
		code = stripUTF8BOM(string(data))
	case "--code-b64":
		decoded, err := decodeBase64Source(flagString(cmd, "code-b64"))
		if err != nil {
			return &evalCodeError{"EVAL_CODE_SOURCE_ERROR", fmt.Sprintf("--code-b64: %v", err)}
		}
		code = decoded
	}
	if strings.TrimSpace(code) == "" {
		return &evalCodeError{"EVAL_CODE_SOURCE_ERROR", used[0] + " carried no code"}
	}
	// Set() also marks the flag Changed, so collectParams ships it and the
	// required-param check passes without a special case.
	return cmd.Flags().Set("code", code)
}

// flagString reads a flag's string value, tolerating an unregistered flag.
func flagString(cmd *cobra.Command, name string) string {
	if f := cmd.Flags().Lookup(name); f == nil {
		return ""
	}
	v, _ := cmd.Flags().GetString(name)
	return v
}

// stripUTF8BOM removes a leading UTF-8 BOM. PowerShell 5.1's
// `Out-File -Encoding utf8` writes one, and a BOM in front of the snippet is
// itself a parse error — the exact silent failure these channels exist to
// remove. Everything else (CRLF included) travels verbatim.
func stripUTF8BOM(s string) string {
	return strings.TrimPrefix(s, "\ufeff")
}

// decodeBase64Source decodes a base64 snippet, tolerating embedded whitespace
// and both the standard/URL-safe alphabets (with or without padding) so a
// line-wrapped value from any shell still works.
func decodeBase64Source(raw string) (string, error) {
	trimmed := strings.Join(strings.Fields(raw), "")
	if trimmed == "" {
		return "", errors.New("empty value")
	}
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	var lastErr error
	for _, enc := range encodings {
		if decoded, err := enc.DecodeString(trimmed); err == nil {
			return stripUTF8BOM(string(decoded)), nil
		} else {
			lastErr = err
		}
	}
	return "", fmt.Errorf("not valid base64 (%v)", lastErr)
}
