#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

PORT="${1:-8080}"
TEST_PORT=18090

# --- Kill anything already on our ports ---

echo "=== Pre-run cleanup ==="
for p in $PORT $TEST_PORT; do
    PIDS=$(lsof -ti ":$p" 2>/dev/null || true)
    if [ -n "$PIDS" ]; then
        echo "killing processes on port $p: $PIDS"
        echo "$PIDS" | xargs kill -9 2>/dev/null || true
    fi
done
sleep 1

# --- Cleanup on exit ---

cleanup() {
    echo ""
    echo "=== Cleaning up ==="
    for p in $PORT $TEST_PORT; do
        PIDS=$(lsof -ti ":$p" 2>/dev/null || true)
        if [ -n "$PIDS" ]; then
            echo "$PIDS" | xargs kill -9 2>/dev/null || true
        fi
    done
    rm -f ./anyserver
}
trap cleanup EXIT

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
echo "=== Starting anyserver on http://localhost:${PORT} ==="
./anyserver -port "$PORT" -name "anyserver" &
SERVER_PID=$!

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
check_status "http://localhost:$PORT/source/" "200" "GET /source/"
check_status "http://localhost:$PORT/docs/" "200" "GET /docs/"
check_status "http://localhost:$PORT/api/" "200" "GET /api/"
check_status "http://localhost:$PORT/api/swagger.json" "200" "GET /api/swagger.json"
check_status "http://localhost:$PORT/static/docs.css" "200" "GET /static/docs.css"
check_status "http://localhost:$PORT/server/" "200" "GET /server/"
check_status "http://localhost:$PORT/wormhole/requests?tail=1" "200" "GET /wormhole/requests?tail=1"
check_status "http://localhost:$PORT/nonexistent" "404" "GET /nonexistent"

echo ""
echo "=== All checks passed ==="

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
echo "Press Ctrl+C to stop the server."
wait "$SERVER_PID"
