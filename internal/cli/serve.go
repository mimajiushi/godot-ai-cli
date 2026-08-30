package cli

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mimajiushi/godot-ai-cli/internal/daemon"
	"github.com/mimajiushi/godot-ai-cli/internal/pluginmeta"
)

// newServeCommand runs the backend (plugin WebSocket + HTTP API) in the
// foreground until Ctrl-C. Editors discover and adopt it through the
// /godot-ai/status endpoint, exactly like the upstream Python server.
func newServeCommand() *cobra.Command {
	var httpPort, wsPort int
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the godot-ai backend (HTTP + plugin WebSocket) in the foreground",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			cfg := daemon.Config{
				HTTPPort: httpPort,
				WSPort:   wsPort,
				Version:  pluginmeta.PluginVersion(),
			}
			d, err := daemon.Start(ctx, cfg)
			if err != nil {
				return jsonError(cmd, "SERVE_START_FAILED", err.Error(), nil)
			}

			// Same port memory as launch (no project context here), so
			// one-shot commands find this daemon without --http-port.
			// Best-effort: the record is a hint, never a failure.
			_ = writeLastDaemon(lastDaemonRecord{HTTPPort: d.HTTPPort(), WSPort: d.WSPort()})

			// One JSON startup line on stdout: agents parse this to learn
			// the bound ports and the advertised server version.
			if err := printJSON(cmd.OutOrStdout(), map[string]any{
				"status":    "listening",
				"http_port": d.HTTPPort(),
				"ws_port":   d.WSPort(),
				"version":   cfg.Version,
			}, false); err != nil {
				return err
			}

			<-d.Done() // returns on Ctrl-C or POST /godot-ai/cli/shutdown
			return nil
		},
	}
	cmd.Flags().IntVar(&httpPort, "http-port", daemon.DefaultHTTPPort, "agent-facing HTTP API port")
	cmd.Flags().IntVar(&wsPort, "ws-port", daemon.DefaultWSPort, "plugin-facing WebSocket port")
	return cmd
}
