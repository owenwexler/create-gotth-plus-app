#!/usr/bin/env bash
# Runs the dev-only POST /seed route. /seed is a mutation, so this first loads the home page to get a
# CSRF cookie and then sends the token back in the X-CSRF-Token header.
#
#   ./seed.sh          # uses Port and SeedAdminKey from .env (port 3000 without one)
#   ./seed.sh 8080     # use another port
set -euo pipefail

cd "$(dirname "$0")"

# env_value <name>: the value of name in .env, without surrounding quotes
env_value() {
  [ -f .env ] || return 0
  sed -n "s/^$1=//p" .env | tail -n 1 | tr -d "\"'\r"
}

PORT="${1:-$(env_value Port)}"
PORT="${PORT:-3000}"
ADMIN_KEY="${SeedAdminKey:-$(env_value SeedAdminKey)}"
BASE_URL="http://localhost:$PORT"

JAR=$(mktemp)
trap 'rm -f "$JAR"' EXIT

curl -fsS -c "$JAR" -o /dev/null "$BASE_URL/"
TOKEN=$(awk '/csrf_token/ {print $7}' "$JAR")

if [ -z "$TOKEN" ]; then
  echo "no CSRF cookie from $BASE_URL/ - is the server running?" >&2
  exit 1
fi

curl -fsS -X POST "$BASE_URL/seed" -b "$JAR" -H "X-CSRF-Token: $TOKEN" -H "X-Admin-Key: $ADMIN_KEY"
echo "seeded $BASE_URL"
