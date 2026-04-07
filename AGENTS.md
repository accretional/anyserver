# Agents

## Setup

Before working on this project, run `./setup.sh` to install required tools:
- protoc plugins (protoc-gen-go, protoc-gen-go-grpc, protoc-gen-grpc-gateway, protoc-gen-openapiv2)
- Third-party proto files (google/api, openapiv2)

## Build & Test

- `./tools/gen.sh` — regenerate all proto code
- `./build.sh` — prepare embedded source + build binary
- `./test.sh` — full validation (vet, test, build, smoke test)

All validation goes through these scripts. Never run go test/build ad-hoc as final validation.
