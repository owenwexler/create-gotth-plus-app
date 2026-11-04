#!/usr/bin/env bash
# Copies the committed files of a gotth-plus checkout into internal/scaffold/_template, which the CLI
# embeds. Only committed files are copied (git archive), so .env, downloaded JS and build output never
# end up in the template.
#
#   ./scripts/sync-template.sh                    # ../gotth-plus at HEAD
#   ./scripts/sync-template.sh ~/src/gotth-plus v1.2.0
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="${1:-$ROOT/../gotth-plus}"
REF="${2:-HEAD}"
DEST="$ROOT/internal/scaffold/_template"

if [ -n "$(git -C "$SRC" status --porcelain)" ]; then
  echo "warning: $SRC has uncommitted changes; they are not copied" >&2
fi

rm -rf "$DEST"
mkdir -p "$DEST"
git -C "$SRC" archive --format=tar "$REF" | tar -x -C "$DEST"

# A directory with a go.mod is a separate module, which go:embed refuses, and the Go tools would
# treat the template as code of this module. The leading underscore of _template keeps ./... out of
# it; renaming go.mod keeps embed working. The generator renames it back.
mv "$DEST/go.mod" "$DEST/go.mod.tmpl"

# commit and date, printed by the CLI so users know which gotth-plus they got
printf '%s %s\n' "$(git -C "$SRC" rev-parse --short "$REF")" "$(git -C "$SRC" log -1 --format=%cs "$REF")" \
  > "$DEST/.gotth-plus-version"

echo "synced $(cat "$DEST/.gotth-plus-version") from $SRC into $DEST"
