#!/usr/bin/env bash
# Generates the *_templ.go files for the .templ files in internal/scaffold/_variants, with exactly the
# templ version in the template's go.mod, so generated projects never need templ to be consistent.
# templ skips directories whose names start with "_", so it runs on a temporary copy.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VARIANTS="$ROOT/internal/scaffold/_variants"
VERSION="$(awk '$1 == "github.com/a-h/templ" {print $2}' "$ROOT/internal/scaffold/_template/go.mod.tmpl")"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

cd "$VARIANTS"
find . -name '*.templ' -print0 | while IFS= read -r -d '' f; do
  mkdir -p "$TMP/$(dirname "$f")"
  cp "$f" "$TMP/$f"
done

# one variant at a time, from its root: templ writes each file's path (relative to where it runs)
# into the generated code, and in a project the variant's root is the project's root
for variant in "$TMP"/*/; do
  (cd "$variant" && go run "github.com/a-h/templ/cmd/templ@$VERSION" generate -path . 2>&1 | grep -v "templ version check" || true)
done

find "$TMP" -name '*_templ.go' -print0 | while IFS= read -r -d '' f; do
  rel="${f#"$TMP"/}"
  cp "$f" "$VARIANTS/$rel"
  echo "generated _variants/$rel (templ $VERSION)"
done
