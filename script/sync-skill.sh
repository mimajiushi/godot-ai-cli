#!/usr/bin/env bash
# Re-sync the bundled agent-skill snapshot (skill/) from the sibling
# workspace source (../godot-ai-skill/). The workspace keeps both
# directories; the repo copy is the release-time snapshot that
# .github/workflows/release.yml zips into godot-ai-skill.zip.
#
# The source tree ships no binaries — it is SKILL.md, scripts/ and
# references/ only — so the sync is a plain delete + copy. Run this before
# tagging a release whenever the skill changed.
#
# Usage (Git Bash, from godot-ai-cli/):
#   bash script/sync-skill.sh
# Override the source location via SKILL_SRC=/path/to/godot-ai-skill.
set -euo pipefail
cd "$(dirname "$0")/.."

SRC="${SKILL_SRC:-../godot-ai-skill}"
DST="skill"

[ -f "$SRC/SKILL.md" ] || {
  echo "sync-skill: ERROR: $SRC/SKILL.md not found — is the sibling skill checkout present?" >&2
  exit 1
}

rm -rf "$DST"
mkdir -p "$DST"
cp -R "$SRC/." "$DST/"

# Strip OS junk if any slipped in with the copy.
find "$DST" -type f \( -name .DS_Store -o -name Thumbs.db -o -name desktop.ini \) -delete

count=$(find "$DST" -type f | wc -l)
echo "sync-skill: synced $count files from $SRC -> $DST"
find "$DST" -type f | sort
