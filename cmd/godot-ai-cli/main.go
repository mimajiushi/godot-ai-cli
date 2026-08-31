// Command godot-ai-cli is the entry point of the godot-ai-cli binary.
//
// godot-ai-cli drives a live Godot editor through the godot_ai editor
// plugin: it can launch the editor itself (headed or headless), serves
// the plugin's WebSocket/HTTP backend protocol, and exposes every plugin
// operation as a plain subcommand so any agent can use it without MCP.
package main

import (
	"os"

	"github.com/mimajiushi/godot-ai-cli/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
