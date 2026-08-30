#!/usr/bin/env bash
# Local rehearsal of .github/workflows/release.yml: build all six platform
# binaries with the release ldflags, package the update-contract zips plus
# the skill bundle into dist/, generate the checksums file, then self-verify
# the result.
#
# Self-verification:
#   1. every platform zip contains exactly one executable at its root
#      (the contract internal/update.ExtractBinary relies on);
#   2. every checksum line validates (sha256sum -c);
#   3. the host-platform binary, extracted fresh from its zip, reports the
#      stamped version via --version.
#
# Usage (Git Bash, from godot-ai-cli/):
#   VERSION=0.1.0 bash script/build-release.sh
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${VERSION:-0.1.0}"
DIST="dist"
MODULE="github.com/mimajiushi/godot-ai-cli"
TARGETS="windows/amd64 windows/arm64 linux/amd64 linux/arm64 darwin/amd64 darwin/arm64"

[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] || {
  echo "build-release: ERROR: VERSION '$VERSION' is not <semver> (x.y.z or x.y.z-<prerelease>)" >&2
  exit 1
}
[ -f skill/SKILL.md ] || {
  echo "build-release: ERROR: skill/SKILL.md missing — run: bash script/sync-skill.sh" >&2
  exit 1
}

PY="$(command -v python || command -v python3 || true)"
command -v zip >/dev/null 2>&1 || [ -n "$PY" ] || {
  echo "build-release: ERROR: need either zip or python/python3 for packaging" >&2
  exit 1
}

# make_zip <out.zip> <dir>: zip the CONTENTS of <dir> (relative paths, OS
# junk excluded) into <out.zip>. Prefers `zip`; falls back to Python's
# zipfile module because Git Bash on Windows ships no zip. Paths stay
# relative to the repo root so both tool flavors resolve them identically.
make_zip() {
  local out="$1" dir="$2"
  rm -f "$out"
  if command -v zip >/dev/null 2>&1; then
    local abs_out="$(pwd -P)/$out"
    (cd "$dir" && zip -q -r "$abs_out" . -x "*.DS_Store" -x "*Thumbs.db" -x "*desktop.ini")
  else
    "$PY" - "$out" "$dir" <<'PYEOF'
import os, sys, zipfile
out, src = sys.argv[1], sys.argv[2]
junk = {".DS_Store", "Thumbs.db", "desktop.ini"}
with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as z:
    for root, dirs, files in os.walk(src):
        dirs.sort()
        for name in sorted(files):
            if name in junk:
                continue
            full = os.path.join(root, name)
            z.write(full, os.path.relpath(full, src).replace(os.sep, "/"))
PYEOF
  fi
}

rm -rf "$DIST"
mkdir -p "$DIST"

echo "== build (version $VERSION) =="
for t in $TARGETS; do
  goos="${t%/*}"; goarch="${t#*/}"
  bin="godot-ai-cli"; [ "$goos" = "windows" ] && bin="godot-ai-cli.exe"
  stage="$DIST/.stage-$goos-$goarch"
  mkdir -p "$stage"
  echo "-- $goos/$goarch"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath \
    -ldflags "-s -w -X $MODULE/internal/version.Version=$VERSION" \
    -o "$stage/$bin" ./cmd/godot-ai-cli
  make_zip "$DIST/godot-ai-cli-$VERSION-$goos-$goarch.zip" "$stage"
  rm -rf "$stage"
done

echo "== checksums + skill zip =="
# `<sha256>  <filename>` lines — the format internal/update.verifyChecksum parses.
(cd "$DIST" && sha256sum godot-ai-cli-*.zip > "godot-ai-cli-$VERSION-checksums.txt")
make_zip "$DIST/godot-ai-skill.zip" "skill"

echo "== self-verify: zip contents =="
for z in "$DIST"/godot-ai-cli-*.zip; do
  names="$(unzip -Z1 "$z")"
  count="$(printf '%s\n' "$names" | wc -l)"
  if [ "$count" -ne 1 ] || { [ "$names" != "godot-ai-cli" ] && [ "$names" != "godot-ai-cli.exe" ]; }; then
    echo "build-release: ERROR: $z must contain exactly one executable at its root, got:" >&2
    printf '%s\n' "$names" >&2
    exit 1
  fi
  echo "   ok $z -> $names"
done

echo "== self-verify: checksums =="
(cd "$DIST" && sha256sum -c "godot-ai-cli-$VERSION-checksums.txt")

echo "== self-verify: stamped version =="
case "$(uname -s)" in
  MINGW*|MSYS*|CYGWIN*) host_zip="godot-ai-cli-$VERSION-windows-amd64.zip"; bin="godot-ai-cli.exe" ;;
  Linux*)               host_zip="godot-ai-cli-$VERSION-linux-amd64.zip";   bin="godot-ai-cli" ;;
  Darwin*)              host_zip="godot-ai-cli-$VERSION-darwin-amd64.zip";  bin="godot-ai-cli" ;;
  *) echo "build-release: ERROR: unsupported host for the --version check" >&2; exit 1 ;;
esac
verify_dir="$DIST/.verify"
mkdir -p "$verify_dir"
unzip -q "$DIST/$host_zip" -d "$verify_dir"
got="$("$verify_dir/$bin" --version | head -1)"
rm -rf "$verify_dir"
echo "   $bin --version: $got"
case "$got" in
  *"version $VERSION"*) ;;
  *) echo "build-release: ERROR: stamped version mismatch (want $VERSION)" >&2; exit 1 ;;
esac

echo "== dist/ =="
ls -la "$DIST"
echo "build-release: OK ($VERSION)"
