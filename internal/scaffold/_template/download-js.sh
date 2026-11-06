#!/usr/bin/env bash
# Downloads the latest htmx, its preload extension, Alpine.js (CSP build) and hx-optimistic into
# static/js so they are self-hosted (the Content-Security-Policy only allows scripts from 'self').
#
# Needs only git and curl: no Node, no npm. Versions are resolved from each project's git tags, or
# for the preload extension, which has no release tags of its own, from jsDelivr.
#
#   ./download-js.sh                       # latest of everything
#   HTMX_VERSION=2.0.11 ./download-js.sh   # pin a version (also PRELOAD_VERSION, ALPINE_VERSION, HX_OPTIMISTIC_VERSION)
#
# htmx stays on its 2.x line by default: app.js and the layout use the htmx 2 config and event names.
# Set HTMX_MAJOR=4 (and port app.js) to move to the next major.
set -euo pipefail

HTMX_MAJOR="${HTMX_MAJOR:-2}"
ALPINE_MAJOR="${ALPINE_MAJOR:-3}"

OUT_DIR="$(cd "$(dirname "$0")" && pwd)/static/js"
mkdir -p "$OUT_DIR"

# latest_version <github owner/repo> <major>: the newest stable vMAJOR.x.y tag, without the "v"
latest_version() {
  local version
  version=$(git ls-remote --tags --refs "https://github.com/$1.git" "v$2.*" |
    sed 's#.*refs/tags/v##' |
    grep -E '^[0-9]+\.[0-9]+\.[0-9]+$' |
    sort -V |
    tail -n 1)

  if [ -z "$version" ]; then
    echo "could not find a v$2.x release tag for $1" >&2
    exit 1
  fi
  echo "$version"
}

# latest_jsdelivr_version <package> <major>: the newest MAJOR.x.y release of a package published on
# jsDelivr, for libraries without release tags on GitHub
latest_jsdelivr_version() {
  local version
  version=$(curl -fsSL "https://data.jsdelivr.com/v1/packages/npm/$1/resolved?specifier=$2" |
    sed -n 's/.*"version": *"\([0-9][0-9.]*\)".*/\1/p' |
    head -n 1)

  if [ -z "$version" ]; then
    echo "could not find a $2.x release of $1" >&2
    exit 1
  fi
  echo "$version"
}

# download <url> <file name>: fetch to a temp file first so a failed download never leaves a broken script
download() {
  local tmp
  tmp=$(mktemp)
  if ! curl -fsSL "$1" -o "$tmp"; then
    rm -f "$tmp"
    echo "download failed: $1" >&2
    exit 1
  fi
  mv "$tmp" "$OUT_DIR/$2"
  chmod 644 "$OUT_DIR/$2"
  echo "  $2  <-  $1"
  echo "  sha384-$(openssl dgst -sha384 -binary "$OUT_DIR/$2" | openssl base64 -A)"
}

HTMX_VERSION="${HTMX_VERSION:-$(latest_version bigskysoftware/htmx "$HTMX_MAJOR")}"
PRELOAD_VERSION="${PRELOAD_VERSION:-$(latest_jsdelivr_version htmx-ext-preload "$HTMX_MAJOR")}"
ALPINE_VERSION="${ALPINE_VERSION:-$(latest_version alpinejs/alpine "$ALPINE_MAJOR")}"
HX_OPTIMISTIC_VERSION="${HX_OPTIMISTIC_VERSION:-$(latest_version lorenseanstewart/hx-optimistic 1)}"

echo "htmx $HTMX_VERSION"
download "https://raw.githubusercontent.com/bigskysoftware/htmx/v$HTMX_VERSION/dist/htmx.min.js" htmx.min.js

# htmx's extensions live in one repository without per-extension release tags, so the preload
# extension comes from its published files on jsDelivr, pinned to the exact version.
echo "htmx-ext-preload $PRELOAD_VERSION"
download "https://cdn.jsdelivr.net/npm/htmx-ext-preload@$PRELOAD_VERSION/dist/preload.min.js" htmx-ext-preload.min.js

# Alpine does not commit or attach its built files on GitHub, so the CSP build comes from the CDN URL
# the Alpine docs point to, pinned to the exact version of the git tag.
echo "alpine (csp build) $ALPINE_VERSION"
download "https://cdn.jsdelivr.net/npm/@alpinejs/csp@$ALPINE_VERSION/dist/cdn.min.js" alpine-csp.min.js

echo "hx-optimistic $HX_OPTIMISTIC_VERSION"
download "https://raw.githubusercontent.com/lorenseanstewart/hx-optimistic/v$HX_OPTIMISTIC_VERSION/hx-optimistic.min.js" hx-optimistic.min.js

echo "done: $OUT_DIR"
