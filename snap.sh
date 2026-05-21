#!/bin/bash
# snap.sh — capture browser screenshots of a running anyserver for visual review.
#
# Idempotent: clears chrome-testing/snapshots/ before each run, picks fresh
# chromerpc ports, requires anyserver to be reachable on $PORT.
#
# Usage:
#   ./snap.sh           # screenshot http://localhost:8080
#   ./snap.sh 9090      # screenshot http://localhost:9090
#   PORT=9090 ./snap.sh
#   NO_OPEN=1 ./snap.sh # skip opening the index snapshot
#
# Wraps chrome-testing/snap.sh (which manages chromerpc + headless Chrome).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

PORT="${1:-${PORT:-8080}}"
BASE_URL="http://localhost:${PORT}"
SNAP_DIR="chrome-testing/snapshots"
SNAP_TOOL="chrome-testing/snap.sh"

echo "=== anyserver snapshot ==="
echo "base URL: $BASE_URL"
echo "output:   $SNAP_DIR/"

if ! curl -s -o /dev/null --max-time 3 "$BASE_URL/"; then
    echo "ERROR: anyserver not reachable at $BASE_URL/" >&2
    echo "Start it first: ./LET_IT_RIP.sh $PORT" >&2
    exit 1
fi

if [ ! -x "$SNAP_TOOL" ]; then
    echo "ERROR: $SNAP_TOOL not found or not executable" >&2
    exit 1
fi

# Clean previous snapshots (idempotent)
rm -rf "$SNAP_DIR"
mkdir -p "$SNAP_DIR"

# Pages to capture: PATH:FILENAME
# `/#terms` snaps the index with the footer frunk open via :target —
# verifies the frunk pops UNDER the footer-base when expanded.
PAGES=(
    "/:index.png"
    "/#terms:index-terms.png"
    "/docs/:docs.png"
    "/api/:api.png"
    "/server/:server.png"
)

echo ""
for entry in "${PAGES[@]}"; do
    path="${entry%%:*}"
    file="${entry##*:}"
    echo "--- $path -> $SNAP_DIR/$file"
    bash "$SNAP_TOOL" "${BASE_URL}${path}" "$SNAP_DIR/$file"
done

echo ""
echo "=== Snapshots ==="
ls -la "$SNAP_DIR"

# Open index snapshot for visual review (skipped in CI / when NO_OPEN=1)
if [ -z "${NO_OPEN:-}" ] && command -v open &>/dev/null; then
    echo ""
    echo "Opening $SNAP_DIR/index.png ..."
    open "$SNAP_DIR/index.png"
fi
