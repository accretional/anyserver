#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

export PATH="$(go env GOPATH)/bin:$PATH"

echo "=== Generating proto code ==="

# --- Generate mime proto ---

echo "  openformat/v1/mime.proto"
protoc \
    -I third_party \
    --go_out=proto --go_opt=paths=source_relative \
    openformat/v1/mime.proto

# --- Generate docs proto ---

echo "  proto/docs/docs.proto"
protoc \
    -I proto/docs \
    -I third_party \
    --go_out=proto/docs --go_opt=paths=source_relative \
    --go-grpc_out=proto/docs --go-grpc_opt=paths=source_relative \
    --grpc-gateway_out=proto/docs --grpc-gateway_opt=paths=source_relative \
    --openapiv2_out=proto/docs \
    docs.proto

# --- Generate metrics proto ---

echo "  proto/metrics/metrics.proto"
protoc \
    -I proto/metrics \
    -I third_party \
    --go_out=proto/metrics --go_opt=paths=source_relative \
    --go-grpc_out=proto/metrics --go-grpc_opt=paths=source_relative \
    --grpc-gateway_out=proto/metrics --grpc-gateway_opt=paths=source_relative \
    --openapiv2_out=proto/metrics \
    metrics.proto

# --- Generate files proto ---

echo "  proto/files/*.proto"
protoc \
    -I proto/files \
    -I third_party \
    --go_out=proto/files --go_opt=paths=source_relative \
    --go-grpc_out=proto/files --go-grpc_opt=paths=source_relative \
    --grpc-gateway_out=proto/files --grpc-gateway_opt=paths=source_relative \
    --openapiv2_out=proto/files \
    resource.proto document.proto media.proto data.proto socket.proto server.proto

echo ""
echo "=== Proto generation complete ==="
