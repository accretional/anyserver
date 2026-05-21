#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

PORT="${1:-8080}"
TEST_PORT=18090

# --- Kill anything already on our ports ---
#
# This script is non-blocking — it leaves the server running in the
# background and exits as soon as snap + browser-open succeed.  Each
# re-invocation finds and kills the prior background server here.

echo "=== Pre-run cleanup ==="
# CRITICAL: filter to LISTEN sockets only.  Without `-sTCP:LISTEN`,
# `lsof -ti :PORT` also returns *client* processes with an established
# connection to that port — which means an open browser tab viewing
# http://localhost:8080 would be killed too.  We only want to kill
# the prior server process, not the user's browser sessions.
for p in $PORT $TEST_PORT; do
    PIDS=$(lsof -ti ":$p" -sTCP:LISTEN 2>/dev/null || true)
    if [ -n "$PIDS" ]; then
        echo "killing listener(s) on port $p: $PIDS"
        echo "$PIDS" | xargs kill -9 2>/dev/null || true
    fi
done
sleep 1

# Only the test port needs trap-cleanup — the main server is meant to
# persist past script exit and is reclaimed by the next run above.
# (Again, LISTEN-only filter so we don't murder browser clients.)
cleanup_test_port() {
    PIDS=$(lsof -ti ":$TEST_PORT" -sTCP:LISTEN 2>/dev/null || true)
    [ -n "$PIDS" ] && echo "$PIDS" | xargs kill -9 2>/dev/null || true
}
trap cleanup_test_port EXIT

# --- Setup ---

echo ""
echo "=== Setup ==="
bash setup.sh

# --- Tests (includes its own build + smoke test) ---

echo ""
echo "=== Tests ==="
bash test.sh

# --- Rebuild for serving (test.sh cleans up the binary) ---

echo ""
echo "=== Rebuild for serving ==="
bash build.sh

# --- Run ---

echo ""
echo "=== Starting anyserver on http://localhost:${PORT} (background) ==="
# Detach fully so the server survives this script's exit.
nohup ./anyserver -port "$PORT" -name "anyserver" > /tmp/anyserver.log 2>&1 &
SERVER_PID=$!
disown "$SERVER_PID" 2>/dev/null || true
echo "server PID: $SERVER_PID  (log: /tmp/anyserver.log)"

# Wait for server
for i in $(seq 1 15); do
    if curl -s -o /dev/null "http://localhost:$PORT/" 2>/dev/null; then
        echo "Server is ready."
        break
    fi
    sleep 0.5
done

# --- Validate ---

echo ""
echo "=== Validating responses ==="

check_status() {
    local url="$1"
    local expected="$2"
    local label="$3"
    STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$url")
    if [ "$STATUS" = "$expected" ]; then
        echo "  $label -> $STATUS OK"
    else
        echo "  $label -> $STATUS FAIL (expected $expected)"
        exit 1
    fi
}

check_status "http://localhost:$PORT/" "200" "GET /"
check_status "http://localhost:$PORT/source/" "302" "GET /source/ (redirects to /)"
check_status "http://localhost:$PORT/source/tree.json" "200" "GET /source/tree.json"
check_status "http://localhost:$PORT/docs/" "200" "GET /docs/"
check_status "http://localhost:$PORT/api/" "200" "GET /api/"
check_status "http://localhost:$PORT/api/swagger.json" "200" "GET /api/swagger.json"
check_status "http://localhost:$PORT/static/base.css" "200" "GET /static/base.css"
check_status "http://localhost:$PORT/static/docs.css" "200" "GET /static/docs.css"
check_status "http://localhost:$PORT/static/app.css" "200" "GET /static/app.css"
check_status "http://localhost:$PORT/server/" "200" "GET /server/"
check_status "http://localhost:$PORT/wormhole/requests?tail=1" "200" "GET /wormhole/requests?tail=1"
check_status "http://localhost:$PORT/nonexistent" "404" "GET /nonexistent"

echo ""
echo "=== All checks passed ==="

# --- Snapshots (chromerpc visual review) ---
# Captures key pages via headless Chrome so both the user (via open) and
# the assistant (via the saved PNGs) can review the rendered UI.
# Skip with SKIP_SNAP=1; suppress the auto-open with NO_OPEN=1.

if [ -z "${SKIP_SNAP:-}" ]; then
    echo ""
    echo "=== Snapshots ==="
    # NO_OPEN=1 so snap.sh writes PNGs without opening an image viewer —
    # the live URL is opened below instead.
    NO_OPEN=1 bash snap.sh "$PORT" || echo "  (snap.sh failed; continuing — see output above)"
fi

# --- Open browser ---

URL="http://localhost:${PORT}"
echo "Opening $URL in browser..."
if command -v open &>/dev/null; then
    open "$URL"
elif command -v xdg-open &>/dev/null; then
    xdg-open "$URL"
else
    echo "  (no browser opener found, visit $URL manually)"
fi

echo ""
echo "Server is running in background (PID $SERVER_PID) on http://localhost:${PORT}."
echo "Re-run ./LET_IT_RIP.sh to rebuild & restart.  Or kill it manually:"
echo "  kill $SERVER_PID"
echo "  or: lsof -ti :${PORT} | xargs kill -9"
