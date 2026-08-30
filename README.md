# godot-ai-cli

[![CI](https://github.com/mimajiushi/godot-ai-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/mimajiushi/godot-ai-cli/actions/workflows/ci.yml)
[![Release](https://github.com/mimajiushi/godot-ai-cli/actions/workflows/release.yml/badge.svg)](https://github.com/mimajiushi/godot-ai-cli/actions/workflows/release.yml)
[![Go version](https://img.shields.io/github/go-mod/go-version/mimajiushi/godot-ai-cli)](go.mod)
[![License: MIT](https://img.shields.io/github/license/mimajiushi/godot-ai-cli)](LICENSE)

Drive a live [Godot](https://godotengine.org/) editor from the command line — no MCP required.

`godot-ai-cli` is a single-binary Go CLI that talks to the bundled `godot_ai` editor
plugin (forked from [hi-godot/godot-ai](https://github.com/hi-godot/godot-ai), MIT).
It installs the plugin into your project, launches the editor itself (headed or
headless), and exposes every editor operation — scenes, nodes, scripts, signals,
UI, materials, animation, particles, cameras, environments, tilemaps, tests,
screenshots and more — as plain subcommands that print JSON. Any agent that can
run a shell command can drive Godot.

- **Supported Godot:** 4.5+ (4.7+ recommended)
- **Platforms:** Windows, macOS, Linux (amd64 & arm64)

## Install

### Download a release (recommended)

Prebuilt binaries for Windows, macOS and Linux (amd64 & arm64) are attached to
every [GitHub release](https://github.com/mimajiushi/godot-ai-cli/releases).
Download `godot-ai-cli-<ver>-<os>-<arch>.zip` for your platform, verify it
against `godot-ai-cli-<ver>-checksums.txt` (`sha256sum -c`), unzip it, and put
`godot-ai-cli` (`godot-ai-cli.exe` on Windows) on your PATH.

### Skill install scripts

The bundled agent skill (`skill/`, also attached to every release as
`godot-ai-skill.zip`) ships platform install scripts that resolve the latest
release, download and checksum-verify the right zip, and install it for you:

```bash
bash scripts/install.sh                                       # Linux / macOS / Git Bash
powershell -ExecutionPolicy Bypass -File scripts/install.ps1  # Windows
```

### Build from source

Requires Go (the toolchain version is pinned in `go.mod`):

```bash
git clone https://github.com/mimajiushi/godot-ai-cli.git
cd godot-ai-cli
go build -o godot-ai-cli ./cmd/godot-ai-cli  # godot-ai-cli.exe on Windows
```

## Quick start

```bash
# Install the plugin into a project, launch the editor and wait until ready
godot-ai-cli launch --project /path/to/project

# Drive the editor
godot-ai-cli status
godot-ai-cli scene get-hierarchy
godot-ai-cli node create --type Camera3D --name MainCamera --parent /Main

# Run the project's GDScript test suites
godot-ai-cli test run
```

Every command and subcommand supports `-h` / `--help`. `godot-ai-cli -v` prints
the CLI version together with the supported Godot range. `godot-ai-cli update`
checks GitHub Releases and offers to update in place.

## How it works

```
agent / shell
    │  godot-ai-cli <subcommand>  →  one JSON object on stdout
    ▼
┌──────────────┐   HTTP 127.0.0.1:8000    ┌─────────────────────────┐
│     CLI      │ ───────────────────────▶ │  daemon (`serve`, the    │
└──────────────┘                          │  backend `launch` spawns)│
                                          └───────────┬─────────────┘
                                                      │ WebSocket 127.0.0.1:9500
                                                      ▼
                                          ┌─────────────────────────┐
                                          │ godot_ai editor plugin  │ ──▶ live Godot editor
                                          └─────────────────────────┘
```

One `godot-ai-cli launch` installs the bundled plugin into the project, spawns
the daemon, opens the editor, and waits for the plugin handshake. Afterwards
every subcommand is a thin HTTP call into the daemon, which routes it over the
WebSocket bridge to the plugin running inside the editor. Everything is
loopback-only. The topology, wire envelope and gating rules are documented in
[docs/architecture.md](docs/architecture.md).

## Command surface

147 editor operations across 26 domains (scene, node, script, signal, UI,
theme, animation, material, resource, tilemap, particles, camera, audio, input,
game, tests, screenshots and more). Ask the binary for the live catalog:

```bash
godot-ai-cli commands                    # text listing
godot-ai-cli commands --json --domain node
godot-ai-cli <domain> <op> -h            # per-op flags, timeouts, write gates
```

The same catalog with prose conventions lives in
[skill/references/commands.md](skill/references/commands.md). Anything without
a typed subcommand is reachable via `godot-ai-cli call <plugin_command>
--params '<json>'`, and `batch execute --file ops.json` runs several plugin
commands atomically.

## Updating

`godot-ai-cli update` queries GitHub Releases for the latest version, compares
it semantically against the running build, and — after a confirmation prompt —
downloads the platform zip, verifies its SHA256 against the release checksums
file, and swaps the executable in place (rename-aside on Windows; the leftover
`.old` binary is removed on the next startup). A restart is required afterwards.

## Development

```bash
go build ./...                 # build
go vet ./...                   # vet
gofmt -l .                     # format check (must print nothing)
go test ./... -count=1         # unit tests
```

Editor-backed testing uses the demo project at `../demo` — a sibling directory
in the development workspace that is **not shipped in this repository**. It
carries a GDScript test suite and fixture scenes, and the scripts under
`script/` (e.g. `script/smoke-e2e.sh`, `script/build-demo-scenes.sh`) drive a
real headless editor against it through the CLI itself.

Releases are fully automated: pushing a `vX.Y.Z` tag runs
[.github/workflows/release.yml](.github/workflows/release.yml). To rehearse a
release locally, run `bash script/sync-skill.sh` then `VERSION=x.y.z bash
script/build-release.sh` (see [CONTRIBUTING.md](CONTRIBUTING.md)).

## Relationship to hi-godot/godot-ai

The editor plugin under `plugin/godot_ai/` is a fork of upstream v3.2.4 with
telemetry removed and the Python server spawn disabled (the Go daemon replaces
the Python backend entirely). Every divergence is marked `godot-ai-cli fork
patch` in the GDScript source and enumerated in
[docs/fork-patches.md](docs/fork-patches.md). Upstream license: MIT, "Godot AI
contributors". See `UPSTREAM-LICENSE.txt`.

## License

MIT — see `LICENSE`.
