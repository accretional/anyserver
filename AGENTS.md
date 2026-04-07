# Agents

## Setup

Before working on this project, run `./setup.sh` to install required tools:
- protoc plugins (protoc-gen-go, protoc-gen-go-grpc, protoc-gen-grpc-gateway, protoc-gen-openapiv2)
- Third-party proto files (google/api, openapiv2)

## Build & Test

- `./tools/gen.sh` — regenerate all proto code
- `./build.sh` — prepare embedded source + build binary
- `./test.sh` — full validation (vet, test, build, smoke test)
- `./LET_IT_RIP.sh` — full pipeline: setup + test + build + serve + open browser

All validation goes through these scripts. Never run go test/build ad-hoc as final validation.

**Scripts must be idempotent.** They kill old servers on their ports, clean up on exit, and work when re-run. Never fix port conflicts or stale processes with one-off commands — update the script to handle it and rerun.

## Build-time tools

- `cmd/swaggerhtml/` — merges OpenAPI specs into static HTML API reference (invoked by build.sh)
- `cmd/logpb/` — captures stdout as BuildLog/TestLog protocol buffer binary (invoked by build.sh and test.sh)

## Important

The README is a living plan/roadmap. Do NOT remove or condense sections without explicit approval from the project owners.
