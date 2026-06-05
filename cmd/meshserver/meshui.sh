#!/usr/bin/env bash
# meshui.sh — end-to-end: build the merged Swagger 2.0 spec for the KVQ service
# mesh and serve it through anyserver's swagger-like UI.
#
# Pipeline:
#   1. meshadapter  -> merge live gRPC reflection (chromerpc :50051) + a SMALL
#                      curated set of FileDescriptorSets -> mesh.swagger.json
#   2. swaggerhtml  -> render mesh.swagger.json -> mesh-api.html (HTML fragment)
#   3. meshserver   -> serve swagger.json + api.html via anyserver.Run on $PORT
#
# Memory discipline: only the curated SMALL descriptor sets are loaded. The big
# ones (datadog/digitalocean/stripe/github/openrouter) are intentionally
# excluded. The pre-existing chromerpc on :50051 is fronted, never started or
# killed by this script.
#
# Usage:
#   ./cmd/meshserver/meshui.sh            # port 8093, default registry dir
#   PORT=9000 REGISTRY=/path ./meshui.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

PORT="${PORT:-8093}"
REFLECT="${REFLECT:-localhost:50051}"          # live mesh backend (chromerpc)
REGISTRY="${REGISTRY:-/tmp/registry-descriptors}"
WORKDIR="${WORKDIR:-/tmp}"
SWAGGER="$WORKDIR/mesh.swagger.json"
APIHTML="$WORKDIR/mesh-api.html"

# Curated SMALL descriptor sets only — do NOT add the giant ones (memory).
CURATED=(
  "$REGISTRY/macos-vision.pb"
  "$REGISTRY/vad.pb"
  "$REGISTRY/oss-aether.pb"
  "$REGISTRY/proto-ip.pb"
)

echo "==> [1/3] meshadapter: merging reflection($REFLECT) + curated descriptor sets"
go run ./cmd/meshadapter \
  -reflect "$REFLECT" \
  -title "KVQ Service Mesh" \
  -out "$SWAGGER" \
  "${CURATED[@]}"

echo "==> [2/3] swaggerhtml: rendering API reference HTML"
go run ./cmd/swaggerhtml "$SWAGGER" > "$APIHTML"

echo "==> [3/3] meshserver: serving mesh UI on :$PORT"
exec go run ./cmd/meshserver \
  -port "$PORT" \
  -name "KVQ Service Mesh" \
  -swagger "$SWAGGER" \
  -apihtml "$APIHTML"
