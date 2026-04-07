# Development Rules

**IMPORTANT: No one-off commands.** All tests, builds, and setup steps MUST go through their respective scripts:
- **Setup**: Run `./setup.sh` to install proto plugins, download third-party protos, and tidy modules.
- **Code generation**: Run `./tools/gen.sh` to regenerate proto code (Go, gRPC, gateway, OpenAPI).
- **Building**: Run `./build.sh` to prepare embedded source and build the binary.
- **Testing**: Run `./test.sh` to vet, test, build, and smoke-test. Never run `go test` directly as final validation.

If something needs to be tested or built, it belongs in a script. If it's not in a script, it doesn't count as tested.

## Web UI philosophy

- HTML + CSS only. No client-side JavaScript unless absolutely necessary.
- If interactivity is needed, use HTMX (declarative, attribute-driven).
- Server-rendered HTML via Go's `html/template`.
- No SPAs, no client-side rendering frameworks, no JSON-in-JS patterns.
