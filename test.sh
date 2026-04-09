#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

TEST_PORT=18090
TEST_LOG="/tmp/anyserver_test.log"

# --- Cleanup on exit ---

cleanup() {
    echo ""
    echo "=== Cleaning up ==="
    PIDS=$(lsof -ti ":$TEST_PORT" 2>/dev/null || true)
    if [ -n "$PIDS" ]; then
        echo "$PIDS" | xargs kill -9 2>/dev/null || true
    fi
    rm -f ./anyserver
}
trap cleanup EXIT

# --- Main test steps (output captured to test log) ---

do_test() {
    echo "=== anyserver tests ==="
    echo "date: $(date -u '+%Y-%m-%d %H:%M:%S UTC')"
    echo "go:   $(go version)"
    echo "os:   $(uname -s) $(uname -m)"
    echo ""

    # --- Pre-run cleanup ---

    echo "--- Pre-run cleanup ---"
    if lsof -ti ":$TEST_PORT" 2>/dev/null; then
        echo "killing existing process on port $TEST_PORT"
        lsof -ti ":$TEST_PORT" | xargs kill -9 2>/dev/null || true
    else
        echo "port $TEST_PORT is free"
    fi
    echo ""

    # --- Ensure embedded placeholders exist for go vet ---

    echo "--- Ensuring embed placeholders ---"
    mkdir -p cmd/anyserver/source cmd/anyserver/static
    # Directories must contain at least one file for go:embed to accept them
    [ "$(ls -A cmd/anyserver/source 2>/dev/null)" ] || touch cmd/anyserver/source/placeholder
    [ "$(ls -A cmd/anyserver/static 2>/dev/null)" ] || touch cmd/anyserver/static/placeholder
    for f in cmd/anyserver/swagger.json cmd/anyserver/api.html cmd/anyserver/docs.html; do
        [ -f "$f" ] || echo '{}' > "$f"
    done
    if [ ! -f cmd/anyserver/build.binarypb ]; then
        go build -o logpb ./cmd/logpb/ && echo "(placeholder)" | ./logpb build cmd/anyserver/build.binarypb && rm -f logpb
    fi
    if [ ! -f cmd/anyserver/tests.binarypb ]; then
        go build -o logpb ./cmd/logpb/ && echo "(placeholder)" | ./logpb test cmd/anyserver/tests.binarypb && rm -f logpb
    fi
    echo ""

    # --- Step 1: go vet ---

    echo "--- go vet ---"
    go vet ./...
    echo "go vet: ok"
    echo ""

    # --- Step 2: Unit tests ---

    echo "--- Unit tests ---"
    go test -v ./...
    echo ""

    # --- Step 3: Build ---

    echo "--- Build ---"
    bash build.sh
    echo ""

    # --- Step 4: Smoke test ---

    echo "--- Smoke test ---"
    ./anyserver -port "$TEST_PORT" -name "anyserver-test" &
    SERVER_PID=$!

    # Wait for server
    for i in $(seq 1 15); do
        if curl -s -o /dev/null "http://localhost:$TEST_PORT/" 2>/dev/null; then
            echo "server ready on port $TEST_PORT (pid $SERVER_PID)"
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
    check_status "http://localhost:$TEST_PORT/server/" "200" "GET /server/"
    check_status "http://localhost:$TEST_PORT/wormhole/" "200" "GET /wormhole/"
    check_status "http://localhost:$TEST_PORT/nonexistent" "404" "GET /nonexistent"

    # Kill the smoke test server so tee can finish
    kill "$SERVER_PID" 2>/dev/null && wait "$SERVER_PID" 2>/dev/null || true

    echo ""
    echo "=== All tests passed ==="
}

# Run tests, capturing all output to both terminal and log file
do_test 2>&1 | tee "$TEST_LOG"

# --- Serialize test log as binarypb ---

go build -o logpb ./cmd/logpb/
./logpb test cmd/anyserver/tests.binarypb < "$TEST_LOG"
rm -f "$TEST_LOG" logpb
