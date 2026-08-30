# Contributing

## Build and test

```bash
go build ./...                 # build everything
go vet ./...                   # vet
gofmt -l .                     # format check (must print nothing)
go test ./... -count=1         # unit tests
```

CI (`.github/workflows/ci.yml`) runs vet, the gofmt check and `go test ./...
-race -count=1` on Ubuntu, Windows and macOS, plus a `CGO_ENABLED=0`
cross-compile check for all six release targets — keep it green.

Editor-backed smoke testing needs the demo project at `../demo`, which lives
in the development workspace but is **not shipped in this repository**. With
it present, `bash script/smoke-e2e.sh` exercises the full loop (launch, ops,
GDScript suites, screenshot probe, stop, EditorSettings restore) against a
real headless editor.

## Conventions

- **English comments everywhere** — Go, GDScript, shell, workflows and docs.
- **JSON output contract**: success prints exactly one JSON object on stdout
  and exits 0. stdout stays machine-clean; diagnostics and prompts go to
  stderr.
- **Error envelope**: failures print
  `{"status":"error","error":{"code","message","data"}}` on stdout and exit 1.
  Codes are machine-readable (`SCREAMING_SNAKE`), `data.retryable` tells
  callers whether a blind retry can help. Daemon HTTP endpoints return the
  same envelope in the body — transport-level HTTP codes are not part of the
  contract.
- **Every command supports `-h` / `--help`**, and the catalog stays
  discoverable through `godot-ai-cli commands [--json]`.
- **No telemetry, ever.** Nothing in the CLI, daemon or plugin may phone
  home; the upstream plugin's telemetry is stripped, not just disabled.
- **Plugin upgrades never delete files.** `plugin/install.go` only
  overwrites; a file a newer plugin version dropped stays behind in
  upgraded projects (a stale `.gd` with a colliding `class_name` can break
  them) — a known, accepted limitation.

## Fork-patch policy

`plugin/godot_ai/` tracks upstream [hi-godot/godot-ai](https://github.com/hi-godot/godot-ai)
v3.2.4 and must stay **minimally divergent** so upstream syncs remain
reviewable:

- Every deviation is gated behind a switch in
  `plugin/godot_ai/utils/fork_config.gd` and marked with a
  `godot-ai-cli fork patch` comment (grep that string to audit).
- Dormant upstream code is kept compiling — guards early-return instead of
  deleting code.
- The full patch list and the sync procedure live in
  [docs/fork-patches.md](docs/fork-patches.md).

## Releasing

1. Sync the bundled skill snapshot into the repo (the workspace keeps the
   source in the sibling `../godot-ai-skill/`; the repo's `skill/` copy is
   the release-time snapshot that `release.yml` zips):

   ```bash
   bash script/sync-skill.sh
   ```

   Commit the result together with your changes.

2. Optionally rehearse the release locally — builds all six platform zips,
   the checksums file and `godot-ai-skill.zip` into `dist/`, then
   self-verifies zip contents, checksums and the stamped version:

   ```bash
   VERSION=x.y.z bash script/build-release.sh
   ```

3. Tag `vX.Y.Z` (or `vX.Y.Z-<prerelease>`) and push the tag.
   `.github/workflows/release.yml` validates the tag, builds with the version
   stamped via ldflags, packages the assets, and publishes the release with
   auto-generated notes. Tags containing a hyphen publish as a prerelease.
   The workflow can also be run manually (Actions → Release → Run workflow)
   for a dry-run that uploads artifacts without creating a release.
