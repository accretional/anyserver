#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

echo "=== anyserver setup ==="

# --- Check Go ---

if ! command -v go &>/dev/null; then
    echo "ERROR: go not found. Install Go 1.26+ from https://go.dev/dl/"
    exit 1
fi
echo "Go: $(go version)"

# --- Check protoc ---

if ! command -v protoc &>/dev/null; then
    echo "ERROR: protoc not found."
    echo "  macOS: brew install protobuf"
    echo "  Linux: apt install -y protobuf-compiler"
    exit 1
fi
echo "protoc: $(protoc --version)"

# --- Install Go protoc plugins ---

GOBIN="${GOBIN:-$(go env GOPATH)/bin}"

install_if_missing() {
    local bin="$1"
    local pkg="$2"
    if ! command -v "$bin" &>/dev/null && [ ! -f "$GOBIN/$bin" ]; then
        echo "Installing $bin..."
        go install "$pkg"
    else
        echo "$bin: found"
    fi
}

install_if_missing protoc-gen-go google.golang.org/protobuf/cmd/protoc-gen-go@latest
install_if_missing protoc-gen-go-grpc google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
install_if_missing protoc-gen-grpc-gateway github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@latest
install_if_missing protoc-gen-openapiv2 github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@latest

# --- Download third-party protos if missing ---

download_if_missing() {
    local path="$1"
    local url="$2"
    if [ ! -f "$path" ]; then
        echo "Downloading $path..."
        mkdir -p "$(dirname "$path")"
        curl -sL -o "$path" "$url"
    fi
}

download_if_missing third_party/google/api/annotations.proto \
    "https://raw.githubusercontent.com/googleapis/googleapis/master/google/api/annotations.proto"
download_if_missing third_party/google/api/http.proto \
    "https://raw.githubusercontent.com/googleapis/googleapis/master/google/api/http.proto"
download_if_missing third_party/protoc-gen-openapiv2/options/annotations.proto \
    "https://raw.githubusercontent.com/grpc-ecosystem/grpc-gateway/main/protoc-gen-openapiv2/options/annotations.proto"
download_if_missing third_party/protoc-gen-openapiv2/options/openapiv2.proto \
    "https://raw.githubusercontent.com/grpc-ecosystem/grpc-gateway/main/protoc-gen-openapiv2/options/openapiv2.proto"

# --- go mod tidy ---

echo ""
echo "Running go mod tidy..."
go mod tidy

echo ""
echo "=== Setup complete ==="
