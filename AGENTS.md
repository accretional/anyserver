# Agents

## Setup

Before working on this project, run `./setup.sh` to install required tools:
- protoc plugins (protoc-gen-go, protoc-gen-go-grpc, protoc-gen-grpc-gateway, protoc-gen-openapiv2)
- Third-party proto files (google/api, openapiv2)

## Build & Test

- `./tools/gen.sh` — regenerate all proto code
- `./build.sh` — prepare embedded source + build binary
- `./test.sh` — full validation (vet, test, build, smoke test)
- `./LET_IT_RIP.sh` — full pipeline: setup + test + build + serve + snap + open browser
- `./snap.sh [port]` — re-snapshot the running server (writes `chrome-testing/snapshots/*.png`, opens index)

All validation goes through these scripts. Never run go test/build ad-hoc as final validation.

**CRITICAL: ALWAYS run `./LET_IT_RIP.sh` before EVERY `git commit` and `git push`.** No exceptions.

**LET_IT_RIP.sh is non-blocking.** It detaches the server with `nohup` and exits as soon as snap + browser-open succeed. Re-invoking it kills the prior server on the port (in the pre-run cleanup block) and rebuilds fresh. Do NOT pipe its output through `tail`/`grep` waiting for an "ended" marker, and do NOT poll for steady-state — by the time the script returns, the server is already up on `:8080` and the new snapshots are in `chrome-testing/snapshots/`.

**Scripts must be idempotent.** They kill old servers on their ports and work when re-run. Never fix port conflicts or stale processes with one-off commands — update the script to handle it and rerun.

## Build-time tools

- `cmd/swaggerhtml/` — merges OpenAPI specs into static HTML API reference (invoked by build.sh)
- `cmd/godochtml/` — generates package documentation HTML from Go source using `go/doc` (invoked by build.sh)
- `cmd/logpb/` — captures stdout as BuildLog/TestLog protocol buffer binary (invoked by build.sh and test.sh)

## Shared page chrome

`internal/ui/` is the single source of truth for the shared top nav and footer used across every server-rendered page:
- `internal/ui/nav.go` — `Nav` const, the top ticker (Home · Source · Docs · API · Server)
- `internal/ui/footer.go` — `Footer` const, the all-footer (status strip + menu + expandable frunk)

When adding a new page, **import `internal/ui` and concat `ui.Nav` / `ui.Footer` into the template** — do not re-inline the markup. Any styling tweaks for shared chrome belong in `static/base.css` (footer + scrollbar + cross-page primitives) or `static/docs.css` (non-app pages).

## Important

The README is a living plan/roadmap. Do NOT remove or condense sections without explicit approval from the project owners.
