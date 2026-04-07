#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

TEST_PORT=18090

echo "=== anyserver tests ==="

# --- Pre-run cleanup ---

echo ""
echo "=== Pre-run cleanup ==="
lsof -ti ":$TEST_PORT" 2>/dev/null | xargs -r kill -9 2>/dev/null || true

# --- Cleanup on exit ---

cleanup() {
    echo ""
    echo "=== Cleaning up ==="
    lsof -ti ":$TEST_PORT" 2>/dev/null | xargs -r kill -9 2>/dev/null || true
    rm -f ./anyserver
}
trap cleanup EXIT

# --- Step 1: go vet ---

echo ""
echo "=== go vet ==="
go vet ./...

# --- Step 2: Unit tests ---

echo ""
echo "=== Unit tests ==="
go test -v ./...

# --- Step 3: Build ---

echo ""
echo "=== Build ==="
bash build.sh

# --- Step 4: Smoke test ---

echo ""
echo "=== Smoke test ==="
./anyserver -port "$TEST_PORT" -name "anyserver-test" &
SERVER_PID=$!

# Wait for server
for i in $(seq 1 15); do
    if curl -s -o /dev/null "http://localhost:$TEST_PORT/" 2>/dev/null; then
        echo "Server is ready."
        break
    fi
    sleep 0.5
done

# Validate endpoints
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

check_status "http://localhost:$TEST_PORT/" "200" "GET /"
check_status "http://localhost:$TEST_PORT/source/" "200" "GET /source/"
check_status "http://localhost:$TEST_PORT/docs/" "200" "GET /docs/"
check_status "http://localhost:$TEST_PORT/api/" "200" "GET /api/"
check_status "http://localhost:$TEST_PORT/api/swagger.json" "200" "GET /api/swagger.json"
check_status "http://localhost:$TEST_PORT/static/docs.css" "200" "GET /static/docs.css"
check_status "http://localhost:$TEST_PORT/nonexistent" "404" "GET /nonexistent"

echo ""
echo "=== All tests passed ==="
