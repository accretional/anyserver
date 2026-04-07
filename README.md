# anyserver

A generic, composable gRPC+HTTP server framework for Go. Anyserver provides:

- **Dual gRPC/HTTP serving** on a single port (via h2c) or separate ports
- **grpc-gateway** REST proxy with auto-generated Swagger UI
- **Service composition**: inject external gRPC services (from other Go modules) into a unified server at build time
- **Auto-generated docs**: godoc HTML embedded in the binary and served via a built-in `Docs` gRPC service
- **Source browsing**: the repo's own source code is `go:embed`-ed and served via `GetSource`/`ListSource` RPCs
- **Polished index page**: README.md rendered above Swagger UI, repo metadata, links to code/docs

## Architecture

```
cmd/anyserver/main.go    ← entry point, uses services.go for registration
services.go              ← top-level service registry (which gRPC services to start)
server/
  server.go              ← dual gRPC/HTTP server logic (Run callback pattern from katarche)
  gateway.go             ← grpc-gateway reverse proxy + Swagger UI serving
proto/docs/
  docs.proto             ← Docs service: GetSource, ListSource, HTML
  docs.pb.go             ← generated
  docs_grpc.pb.go        ← generated
  docs.pb.gw.go          ← grpc-gateway generated
  docs.swagger.json      ← OpenAPI spec generated
internal/docs/
  service.go             ← Docs service implementation (go:embed source + godoc HTML)
static/
  index.html             ← Swagger UI + README.md + metadata
  style.css              ← styling
  swagger-ui/            ← Swagger UI assets
tools/
  gen.sh                 ← protoc + gateway + openapi codegen, auto-generates service registration
```

## Default Service: Docs

The built-in `Docs` service provides three RPCs:

```protobuf
service Docs {
  rpc GetSource(SourceRequest) returns (SourceResponse);   // Get file contents by path
  rpc ListSource(SourceRequest) returns (SourceListResponse); // List files/dirs at path
  rpc HTML(DocRequest) returns (DocResponse);               // Get auto-generated godoc HTML
}
```

All three are also exposed as REST endpoints via grpc-gateway annotations.

Source embedding includes the full repo (including `.git/`) minus build artifacts, binaries, and large generated files. Godoc HTML is generated at build time by `godoc-gen` and embedded alongside.

## Service Composition Pattern

External Go modules expose a registration function:

```go
// In github.com/accretional/vad/pkg/server/register.go
func Register(s *grpc.Server, opts ...Option) error {
    // wire up VoiceSegmentation service with model, config, etc.
}
```

Anyserver's `tools/gen.sh` (inspired by katarche's `gen_go.sh`) can:
1. Clone/import external modules
2. Discover `*_grpc.pb.go` files and `Register()` functions
3. Auto-generate the service registration in `services.go` / `main.go`
4. Run godoc-gen on all packages and embed the output

Consumer repos (like `vad`) use this pattern in their Dockerfile:
```dockerfile
# Clone anyserver, inject this repo's service, build unified binary
RUN git clone https://github.com/accretional/anyserver /anyserver
COPY . /app
RUN cd /anyserver && ./tools/gen.sh --inject /app && go build -o /server ./cmd/anyserver
```

## Prior Art & Patterns Borrowed

### katarche
- `server.Run(grpcPort, httpPort, func(s *grpc.Server) { ... })` — callback-based service registration
- `tools/gen_go.sh` — three-phase codegen: rewrite proto `go_package` → run protoc → scan `*_grpc.pb.go` for `RegisterXyzServer()` and auto-generate main.go
- `tools/go_pull.sh` — import external protos into organized packages, create stub service dirs
- HTTP reflection UI via `DiscoverRPCs()` + dynamic form generation from proto descriptors
- gRPC reflection enabled for runtime introspection

### petros
- `registerServer(grpcServer)` — central function registering 9+ services at once
- Wrapper pattern for external services (CollectionServiceWrapper, etc.) to avoid method conflicts
- Custom HTTP-to-gRPC `/rpc-proxy/` bridge via reflection (same approach as katarche)
- Dual-port architecture (50051 gRPC, 3000 HTTP) with `PortManager` for conflict prevention

### gluon
- Go interface → gRPC full codegen: `FullBootstrap(module, src)` pipeline
- AST toolkit (`astkit`) for type utilities, field ops, node builders, imports, function/struct helpers
- Proto compiler wrapper: runs `protoc` + `protoc-gen-go` + `protoc-gen-go-grpc`
- Round-trip verification: re-analyzes generated code to ensure structural integrity
- Direction: Go → gRPC (opposite of what anyserver needs for external service import, but AST toolkit is reusable)

### godoc-gen
- CLI: `godoc-gen -output ./docs [-title "..."] [-single] [-preview] /path/to/repo1 /path/to/repo2`
- Generates static HTML via `go/parser` + `go/doc` AST parsing, styled like pkg.go.dev
- Single-file mode: self-contained HTML with embedded CSS/JS and package data as JSON
- Multi-file mode: `index.html` homepage + individual package pages under `pkg/`
- Built-in preview server on port 8090

### ffmpeg-proto / audio-visualizer
- `MediaConverter` gRPC service with 4 RPCs:
  - `conversion_stream` (bidirectional) — FFmpeg transforms on media chunks
  - `audio_to_vectors` (client-stream) — audio → amplitude vectors (x=time, y=RMS 0–1)
  - `svg` (client-stream) — vectors → SVG waveform visualization
  - `svg_to_sqlite` (unary) — SVG → vectors for persistence
- 10 transform types via `oneof`: crop, scale, overlay, gif, slideshow, concat, mix, tempo, trim, insert
- Typed Go client in `pkg/client/` with high-level methods: `AudioWaveform()`, `AudioToVectors()`, `VectorsToSvg()`
- Session-based temp file management (UUID isolation under `/tmp/`)

## Plan / Next Steps

### Phase 1: Bootstrap anyserver
- [x] Create repo
- [ ] **1a.** Set up Go module, `server/` package with dual gRPC/HTTP logic (borrow katarche's `server.Run()` callback pattern), `cmd/anyserver/main.go`, `services.go`
- [ ] **1b.** Define `docs.proto` with `Docs` service (GetSource, ListSource, HTML). Add grpc-gateway HTTP annotations and generate Go code + gateway + OpenAPI spec
- [ ] **1c.** Implement Docs service: `go:embed` repo source (minus build artifacts, including `.git/`), serve via GetSource/ListSource. HTML serves godoc-gen output (run `godoc-gen -single -output internal/docs/generated/` at build time)
- [ ] **1d.** Wire grpc-gateway reverse proxy into the HTTP server alongside static file serving. Swagger UI served from embedded assets
- [ ] **1e.** Build index.html: rendered README.md content (embedded at build), Swagger UI pointed at generated OpenAPI spec, repo name as title/header, metadata links to GitHub. Add style.css
- [ ] **1f.** Add `tools/gen.sh`: runs protoc with go/grpc/gateway/openapi plugins, runs godoc-gen, optionally auto-generates service registration by scanning `*_grpc.pb.go` files (borrowing katarche's gen_go.sh phase 3 approach)

### Phase 2: Service composition / linking
- [ ] **2a.** Design service injection: `tools/gen.sh --inject /path/to/module` clones external module, discovers its `*_grpc.pb.go` files, extracts `RegisterXyzServer()` calls, auto-generates the wiring in `services.go`. Avoid petros's wrapper pattern where possible — prefer direct registration. If method conflicts arise, use the wrapper approach as fallback
- [ ] **2b.** Make `vad` export a clean registration entry point: `pkg/server/register.go` with `Register(s *grpc.Server, opts ...Option) error` that initializes ONNX Runtime, loads model, creates server, registers VoiceSegmentation
- [ ] **2c.** Update vad's Dockerfile to: clone anyserver → `gen.sh --inject .` → build unified binary containing both Docs and VoiceSegmentation services. Validate all existing vad tests still pass

### Phase 3: Docs and enhanced audio tooling
- [ ] **3a.** Extend godoc-gen if needed for anyserver integration (ensure single-file mode output can be embedded cleanly, add any missing features for multi-module docs)
- [ ] **3b.** Integrate ffmpeg-proto as a composable service via the injection pattern: its `MediaConverter` service gets registered alongside Docs and VoiceSegmentation. Gateway annotations expose audio conversion as REST endpoints
- [ ] **3c.** Enhanced vad web UI: use ffmpeg-proto's `audio_to_vectors` + `svg` RPCs for server-side waveform visualization of uploaded audio. Add client-side download buttons for segmented chunks. Display SVG waveforms inline in results
- [ ] **3d.** Audio format handling: use ffmpeg-proto's `conversion_stream` for server-side decoding of any audio format to 16kHz PCM (currently client-side only via Web Audio API). This enables gRPC clients (not just browser) to send MP3/WAV directly

### Phase 4: Validation
- [ ] **4a.** anyserver builds and runs standalone with just the Docs service
- [ ] **4b.** vad builds with anyserver composition — all existing vad tests pass
- [ ] **4c.** ffmpeg-proto composes cleanly — waveform generation works end-to-end
- [ ] **4d.** Swagger UI shows all services, godoc HTML is browsable, source is viewable

## Related Projects

- [katarche](https://github.com/accretional/katarche) — unified gRPC service host with HTTP reflection UI (server pattern origin)
- [petros](https://github.com/accretional/petros) — multi-service gRPC host with custom HTTP proxy and wrapper pattern
- [gluon](https://github.com/accretional/gluon) — Go interface → gRPC codegen, AST toolkit, proto compiler
- [godoc-gen](https://github.com/accretional/godoc-gen) — auto-generate godoc HTML from Go packages (CLI, static HTML)
- [ffmpeg-proto](https://github.com/accretional/ffmpeg-proto) — FFmpeg as a gRPC service (MediaConverter: conversion, waveforms, SVG)
- [audio-visualizer](https://github.com/accretional/audio-visualizer) — audio waveform visualization (same codebase as ffmpeg-proto)
- [vad](https://github.com/accretional/vad) — pyannote VAD via ONNX (first consumer of anyserver)
