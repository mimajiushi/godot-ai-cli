package cli

import (
	"errors"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/mimajiushi/godot-ai-cli/internal/update"
	"github.com/mimajiushi/godot-ai-cli/internal/version"
)

// updateAPIBase is the GitHub API root `update` queries; a var so tests can
// point the command at an httptest server (same pattern as daemonctl's
// spawnServe).
var updateAPIBase = update.DefaultAPIBase

// newUpdateCommand implements the `update` self-update flow.
func newUpdateCommand() *cobra.Command {
	var (
		yes     bool
		fromDir string
		check   bool
		proxy   string
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Check GitHub Releases for a newer godot-ai-cli and update in place",
		Long: `update queries the newest GitHub release of godot-ai-cli — stable
and prerelease tags both count (the releases list endpoint is used because
GitHub's "latest" excludes prereleases). When a newer
version exists it offers to download the asset for this platform,
verifies its SHA256 against the release checksums, and only then
replaces the running executable — a checksum mismatch aborts without
touching the install.

On Windows the current binary is renamed to <exe>.old first (a running
executable cannot be overwritten); the next startup removes the
leftover. On Unix the binary is replaced atomically. A FAILED download
never performs that rename — no half-finished install is left behind.

Downloads retry up to 3 times with exponential backoff; a failure
reports machine-readable diagnostics (url, http_status, content_length,
bytes_read, redirect_host, proxy_used, attempts) plus the actionable
next steps (an explicit --proxy retry, or manual install).

--proxy selects a download proxy: an explicit URL
(http://127.0.0.1:7897) or "auto" — environment HTTPS_PROXY/HTTP_PROXY
first, then the Windows system proxy (registry) when unset. Without
--proxy the Go default applies (environment variables only) — note a
TUN/fake-ip setup may still break Go's downloads where the system
stack works, which is exactly what --proxy auto is for.

--check stops after the availability answer: {"status":"ok",
"update_available":true, ...release details} — nothing is downloaded
or replaced.

The update applies only after an interactive confirmation; --yes skips
the prompt. Without a terminal there is no prompt: the result is
"cancelled" together with the release details — re-run with --yes to
apply.

Examples:
  godot-ai-cli update
  godot-ai-cli update --yes
  godot-ai-cli update --check
  godot-ai-cli update --yes --proxy auto`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := update.Run(cmd.Context(), update.Options{
				CurrentVersion: version.Version,
				BaseURL:        updateAPIBase,
				InstallDir:     fromDir,
				AssumeYes:      yes,
				CheckOnly:      check,
				Proxy:          proxy,
				In:             cmd.InOrStdin(),
				IsTerminal:     stdinIsTerminal(cmd.InOrStdin()),
				PromptOut:      cmd.ErrOrStderr(),
			})
			if err != nil {
				var uerr *update.Error
				if errors.As(err, &uerr) {
					return jsonError(cmd, uerr.Code, uerr.Message, uerr.Data)
				}
				return jsonError(cmd, "UPDATE_FAILED", err.Error(), nil)
			}
			return printJSON(cmd.OutOrStdout(), result, prettyOutput)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "apply the update without the interactive confirmation")
	cmd.Flags().BoolVar(&check, "check", false, "only report whether a newer version exists (no download, no replace)")
	cmd.Flags().StringVar(&proxy, "proxy", "", `download proxy: an explicit URL (http://127.0.0.1:7897) or "auto" (HTTPS_PROXY/HTTP_PROXY env, then the Windows system proxy)`)
	cmd.Flags().StringVar(&fromDir, "from", "", "update the godot-ai-cli install in this directory instead of the running executable")
	// --from exists so tests can drive the replace mechanics against a fake
	// install dir; hidden because end users should never need it.
	_ = cmd.Flags().MarkHidden("from")
	return cmd
}

// stdinIsTerminal reports whether r is an interactive terminal. Only an
// *os.File can be one; the character-device bit is the portable check and
// saves a golang.org/x/term dependency.
func stdinIsTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
