#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

BUILD_LOG="/tmp/anyserver_build.log"

# --- Main build steps (output captured to build log) ---

do_build() {
    echo "=== anyserver build ==="
    echo "date: $(date -u '+%Y-%m-%d %H:%M:%S UTC')"
    echo "go:   $(go version)"
    echo "os:   $(uname -s) $(uname -m)"
    echo ""

    # --- Build logpb tool first (needed to capture logs as binarypb) ---

    echo "--- Building logpb tool ---"
    go build -v -o logpb ./cmd/logpb/
    echo ""

    # --- Create placeholder binarypb files if they don't exist ---

    if [ ! -f cmd/anyserver/build.binarypb ]; then
        echo "creating placeholder build.binarypb (first build)"
        echo "(first build)" | ./logpb build cmd/anyserver/build.binarypb
    else
        echo "build.binarypb exists ($(wc -c < cmd/anyserver/build.binarypb) bytes)"
    fi
    if [ ! -f cmd/anyserver/tests.binarypb ]; then
        echo "creating placeholder tests.binarypb"
        echo "(no test log yet)" | ./logpb test cmd/anyserver/tests.binarypb
    else
        echo "tests.binarypb exists ($(wc -c < cmd/anyserver/tests.binarypb) bytes)"
    fi
    echo ""

    # --- Prepare embedded source ---

    EMBED_SRC="cmd/anyserver/source"
    EMBED_STATIC="cmd/anyserver/static"

    echo "--- Preparing embedded source ---"
    rm -rf "$EMBED_SRC" "$EMBED_STATIC"
    mkdir -p "$EMBED_SRC" "$EMBED_STATIC"

    echo "rsync source -> $EMBED_SRC/"
    rsync -a --stats \
        --exclude='cmd/anyserver/source' \
        --exclude='cmd/anyserver/static' \
        --exclude='go.mod' \
        --exclude='go.sum' \
        --exclude='anyserver' \
        --exclude='logpb' \
        --exclude='godochtml' \
        --exclude='*.exe' \
        --exclude='*.test' \
        --exclude='*.out' \
        --exclude='*.binarypb' \
        --exclude='vendor/' \
        --exclude='node_modules/' \
        --exclude='.DS_Store' \
        ./ "$EMBED_SRC/"
    echo ""

    echo "--- Copying static assets ---"
    ls -l static/*.css
    cp static/*.css "$EMBED_STATIC/"
    echo ""

    echo "--- Copying swagger spec ---"
    ls -l proto/docs/docs.swagger.json proto/metrics/metrics.swagger.json
    cp proto/docs/docs.swagger.json cmd/anyserver/swagger.json
    echo ""

    echo "--- Generating API reference HTML ---"
    go run ./cmd/swaggerhtml/ proto/docs/docs.swagger.json proto/metrics/metrics.swagger.json > cmd/anyserver/api.html
    echo "wrote cmd/anyserver/api.html ($(wc -c < cmd/anyserver/api.html) bytes)"
    echo ""

    echo "--- Generating package documentation HTML ---"
    go run ./cmd/godochtml/ -module github.com/accretional/anyserver . > cmd/anyserver/docs.html
    echo "wrote cmd/anyserver/docs.html ($(wc -c < cmd/anyserver/docs.html) bytes)"
    echo ""

    # --- Build binary ---

    echo "--- Building anyserver binary ---"
    go build -v -ldflags="-s -w" -o anyserver ./cmd/anyserver/
    ls -lh anyserver
    echo ""
    echo "=== Build complete ==="
}

# Run build, capturing all output to both terminal and log file
do_build 2>&1 | tee "$BUILD_LOG"

# --- Serialize build log as binarypb (embedded on next build) ---

go build -o logpb ./cmd/logpb/
./logpb build cmd/anyserver/build.binarypb < "$BUILD_LOG"
rm -f "$BUILD_LOG" logpb
