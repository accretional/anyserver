#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

echo "=== anyserver build ==="

# --- Prepare embedded source ---
# Copy the repo source into cmd/anyserver/source/ for go:embed
# Exclude build artifacts, binaries, and large generated files

EMBED_SRC="cmd/anyserver/source"
EMBED_STATIC="cmd/anyserver/static"

rm -rf "$EMBED_SRC" "$EMBED_STATIC"
mkdir -p "$EMBED_SRC" "$EMBED_STATIC"

echo "Copying source for embedding..."
rsync -a \
    --exclude='cmd/anyserver/source' \
    --exclude='cmd/anyserver/static' \
    --exclude='go.mod' \
    --exclude='go.sum' \
    --exclude='anyserver' \
    --exclude='*.exe' \
    --exclude='*.test' \
    --exclude='*.out' \
    --exclude='vendor/' \
    --exclude='node_modules/' \
    --exclude='.DS_Store' \
    ./ "$EMBED_SRC/"

echo "Copying static assets..."
cp static/*.css "$EMBED_STATIC/"

echo "Copying swagger spec..."
cp proto/docs/docs.swagger.json cmd/anyserver/swagger.json

echo "Generating API reference HTML..."
go run ./cmd/swaggerhtml/ proto/docs/docs.swagger.json proto/metrics/metrics.swagger.json > cmd/anyserver/api.html

echo "Generating package documentation HTML..."
go run ./cmd/godochtml/ -module github.com/accretional/anyserver . > cmd/anyserver/docs.html

# --- Build logpb tool (for capturing build/test output as binarypb) ---

echo "Building logpb tool..."
go build -o logpb ./cmd/logpb/

# --- Create placeholder binarypb files if they don't exist ---
# (These get overwritten by build/test output capture, but must exist for go:embed)

if [ ! -f cmd/anyserver/build.binarypb ]; then
    echo "(no build log yet)" | ./logpb build cmd/anyserver/build.binarypb
fi
if [ ! -f cmd/anyserver/tests.binarypb ]; then
    echo "(no test log yet)" | ./logpb test cmd/anyserver/tests.binarypb
fi

# --- Build ---

# Capture the full build log (go build is silent on success, so include our own steps)
{
    echo "=== anyserver build ==="
    echo "date: $(date -u '+%Y-%m-%d %H:%M:%S UTC')"
    echo "go: $(go version)"
    echo ""
    echo "steps:"
    echo "  - rsync source to cmd/anyserver/source/"
    echo "  - copy static assets"
    echo "  - copy swagger spec"
    echo "  - generate API reference HTML (swaggerhtml)"
    echo "  - generate package documentation HTML (godochtml)"
    echo "  - go build -ldflags=\"-s -w\" -o anyserver ./cmd/anyserver/"
    echo ""
    echo "building..."
} > /tmp/anyserver_build.log

go build -ldflags="-s -w" -o anyserver ./cmd/anyserver/ 2>&1 | tee -a /tmp/anyserver_build.log

echo "done." >> /tmp/anyserver_build.log

# Write build log as binarypb (will be embedded on NEXT build)
cat /tmp/anyserver_build.log | ./logpb build cmd/anyserver/build.binarypb
rm -f /tmp/anyserver_build.log logpb

echo ""
echo "=== Build complete: ./anyserver ==="
