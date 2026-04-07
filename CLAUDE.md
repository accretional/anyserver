# Development Rules

**IMPORTANT: No one-off commands.** All tests, builds, and setup steps MUST go through their respective scripts:
- **Setup**: Run `./setup.sh` to install proto plugins, download third-party protos, and tidy modules.
- **Code generation**: Run `./tools/gen.sh` to regenerate proto code (Go, gRPC, gateway, OpenAPI).
- **Building**: Run `./build.sh` to prepare embedded source and build the binary.
- **Testing**: Run `./test.sh` to vet, test, build, and smoke-test. Never run `go test` directly as final validation.
- **Full pipeline + browser**: Run `./LET_IT_RIP.sh` for setup + build + test + serve + browser open.

If something needs to be tested or built, it belongs in a script. If it's not in a script, it doesn't count as tested.

**Scripts must be idempotent.** They must kill old servers on their ports before starting, clean up on exit, and work correctly when re-run without manual intervention. Never fix things with one-off commands outside the scripts — always update the script and rerun it.

Quick `go build ./...` or `go vet ./...` during development to catch compile errors is fine, but the FINAL validation before committing must ALWAYS go through the scripts.

**CRITICAL: ALWAYS run `./LET_IT_RIP.sh` before EVERY `git commit` and `git push`.** No exceptions. Do not commit without running it. Do not push without running it. If you just ran it and are about to push, that is fine. If you are not sure whether you ran it, run it again. The embedded build/test logs, generated HTML, and smoke tests all depend on this pipeline running to completion.

**Build-time tools:**
- `cmd/swaggerhtml/` — merges OpenAPI specs into static HTML for the `/api/` page (run by build.sh)
- `cmd/godochtml/` — generates package documentation HTML from Go source using `go/doc` (run by build.sh)
- `cmd/logpb/` — serializes stdout to BuildLog/TestLog binarypb (run by build.sh and test.sh)

**README is a plan/roadmap.** Do NOT remove, rewrite, or condense sections without explicit approval. When adding new work, update existing sections — do not reorganize or trim.

## Web UI philosophy

- HTML + CSS only. No client-side JavaScript unless absolutely necessary.
- If interactivity is needed, use HTMX (declarative, attribute-driven).
- Server-rendered HTML via Go's `html/template`.
- No SPAs, no client-side rendering frameworks, no JSON-in-JS patterns.
