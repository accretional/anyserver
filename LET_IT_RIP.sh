#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

PORT="${1:-8080}"

# --- Setup ---

echo "=== Setup ==="
bash setup.sh

# --- Build ---

echo ""
echo "=== Build ==="
bash build.sh

# --- Tests ---

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

cleanup() {
    echo ""
    echo "Stopping server (PID $SERVER_PID)..."
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
    rm -f ./anyserver
}
trap cleanup EXIT

# Wait for server
for i in $(seq 1 15); do
    if curl -s -o /dev/null "http://localhost:$PORT/" 2>/dev/null; then
        echo "Server is ready."
        break
    fi
    sleep 0.5
done

# Open browser
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
