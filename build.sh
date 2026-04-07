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

# --- Build ---

echo "Building anyserver..."
go build -ldflags="-s -w" -o anyserver ./cmd/anyserver/

echo ""
echo "=== Build complete: ./anyserver ==="
