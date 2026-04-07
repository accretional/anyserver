# anyserver

> **NOTE**: This README serves as a living plan/roadmap/notes document, not just current-state documentation.
> It includes architecture plans, prior art references, and future phases that have not been implemented yet.
> **LLMs: Do NOT remove, rewrite, or condense sections of this document without explicit approval from the project owners.**
> When adding new work, append to or update existing sections — do not reorganize or trim the plan.

A generic, composable gRPC+HTTP server framework for Go. Anyserver provides:

- **Dual gRPC/HTTP serving** on a single port (via h2c) or separate ports
- **grpc-gateway** REST proxy with auto-generated Swagger UI
- **Service composition**: inject external gRPC services (from other Go modules) into a unified server at build time
- **Source browsing**: the repo's own source code is `go:embed`-ed and served via a streaming `Source` RPC on the `Docs` gRPC service
- **Auto-generated docs**: godoc HTML served over HTTP only (not via gRPC), with navigation linking to source browsing
- **Server info page**: runtime stats, request counters, boot/build/test logs via `Metrics` gRPC service
- **Build-time API reference**: OpenAPI specs rendered to static HTML at build time (pure HTML+CSS, no JavaScript)
- **Polished index page**: README.md rendered above Swagger UI, repo metadata, links to code/docs

## Architecture

```
cmd/anyserver/main.go    <- entry point, uses services.go for registration
services.go              <- top-level service registry (which gRPC services to start)
anyserver.go             <- library entry point: Config + Run() wires all services
server/
  server.go              <- dual gRPC/HTTP server logic (Run callback pattern from katarche)
  gateway.go             <- grpc-gateway reverse proxy + Swagger UI serving
  httpproxy.go           <- gRPC->HTTP proxy: renders SourceCode stream as HTML per type
proto/docs/
  docs.proto             <- Docs service: Source RPC (streaming, typed responses)
  docs.pb.go             <- generated
  docs_grpc.pb.go        <- generated
  docs.pb.gw.go          <- grpc-gateway generated
  docs.swagger.json      <- OpenAPI spec generated
proto/metrics/
  metrics.proto          <- Metrics service: Static/Active/Lifetime/Historical RPCs + BuildLog/TestLog/BootLog
  metrics.pb.go          <- generated
  metrics_grpc.pb.go     <- generated
  metrics.pb.gw.go       <- grpc-gateway generated
  metrics.swagger.json   <- OpenAPI spec generated
internal/docs/
  service.go             <- Docs service implementation (go:embed source)
  httphandler.go         <- HTML source browser (directory listing, code view, media)
internal/metrics/
  service.go             <- Metrics service implementation (runtime stats, request counters, build/test/boot logs)
  httphandler.go         <- Server info HTML page (pure HTML+CSS)
metrics/
  request_counter.go     <- HTTP middleware tracking requests by path and status code
  procfs.go              <- Go runtime stats (goroutines, heap, GC); TODO: Linux procfs
cmd/swaggerhtml/
  main.go                <- build tool: merges OpenAPI specs into static HTML reference page
cmd/logpb/
  main.go                <- build tool: serializes stdout to BuildLog/TestLog binarypb
static/
  docs.css               <- styling for all pages
http/
  *.textproto            <- HTTP.textproto files: templatized HTTP responses per gRPC response type
tools/
  gen.sh                 <- protoc + gateway + openapi codegen, auto-generates service registration
```

## Quick Start

```bash
./setup.sh        # install protoc plugins, download third-party protos
./build.sh        # embed source + static assets, generate API HTML, build binary
./test.sh         # vet, test, build, smoke test (validates all endpoints)
./LET_IT_RIP.sh   # full pipeline: setup + test + build + serve + open browser
```

## Default Service: Docs

The `Docs` service provides a single streaming RPC for source browsing:

```protobuf
import "openformat/v1/mime.proto";

service Docs {
  // Browse embedded source code. When path is a directory, streams Path entries.
  // When path is a file, streams typed content chunks.
  rpc Source(SourceRequest) returns (stream SourceCode);
}

message SourceCode {
  oneof kind {
    Path path = 1;       // Directory listing entry (file or subdirectory)
    Code code = 2;       // Source code / text file chunk
    Media media = 3;     // Image, audio, video, etc.
    Data data = 4;       // Generic streamed data (large files, mixed content)
    Binary binary = 5;   // Opaque binary (triggers download over HTTP)
  }
}

message Path {
  string name = 1;
  bool is_dir = 2;
  int64 size = 3;
}

message Code {
  string contents = 1;   // Text chunk (sequential chunks concatenate to full file)
  string filename = 2;   // Original filename (used for language detection)
}

message Media {
  bytes contents = 1;
  string filename = 2;
  openformat.v1.MimeType mime_type = 3;
}

message Data {
  oneof kind {
    string type_url = 1;   // Describes the data format
    string text = 2;       // Human-readable label/description
    string file_name = 3;  // Associated filename
    bool continue = 4;     // More chunks follow
  }
  bytes contents = 5;
}

message Binary {
  bytes contents = 1;
}
```

### Behavior by path type

| Input path | Response stream |
|-----------|----------------|
| Directory | Stream of `Path` messages (one per entry in the directory) |
| `.go`, `.proto`, `.sh`, `.md`, `.js`, `.css`, `.html`, `.json`, `.yaml`, `.toml`, text files | Stream of `Code` messages (sequential chunks that concatenate to the full file) |
| `.png`, `.jpg`, `.svg`, `.mp3`, `.wav`, `.mp4`, etc. | Stream of `Media` messages with appropriate `MimeType` from [mime-proto](https://github.com/accretional/mime-proto) |
| Large files, mixed content | Stream of `Data` messages with metadata |
| Unknown binary formats | Stream of `Binary` messages |

Source embedding includes the full repo (including `.git/`) minus build artifacts, binaries, and large generated files.

## Metrics Service

The `Metrics` service provides server introspection:

```protobuf
service Metrics {
  rpc Static(StaticRequest) returns (StaticResponse);      // build-time metadata (never changes)
  rpc Active(ActiveRequest) returns (ActiveResponse);      // live runtime state
  rpc Lifetime(LifetimeRequest) returns (LifetimeResponse); // cumulative counters
  rpc Historical(HistoricalRequest) returns (HistoricalResponse); // time-series (TODO)
}

message BuildLog { string stdout = 1; }
message TestLog { string stdout = 1; }
enum BootStatus { BOOT_UNKNOWN = 0; BOOT_STARTED = 1; BOOT_COMPLETE = 2; }
message BootEvent { BootStatus status = 1; google.protobuf.Timestamp timestamp = 2; }
message BootLog { repeated BootEvent events = 1; }
```

| RPC | Description |
|-----|-------------|
| `Static` | Build-time metadata: hostname, port, Go version, OS/arch, build/test/boot logs |
| `Active` | Live runtime state: goroutines, heap, sys memory, GC cycles |
| `Lifetime` | Cumulative counters: uptime, total requests, requests by path/status |
| `Historical` | Time-series data (TODO: implement time-series buckets) |

- **Active TODO**: procfs on Linux — CPU time, open FDs, VmRSS, threads (fall back to runtime stats on non-Linux)

Build/test output is captured during `build.sh`/`test.sh` via `cmd/logpb` and serialized as protocol buffer binary, then embedded in the binary via `go:embed`.

### Server Info Page (`/server/`)

Pure HTML+CSS page (via Go `html/template`) showing:
- Server info table (hostname, port, Go version, OS/arch, uptime)
- Runtime stats (goroutines, heap, sys memory, GC cycles)
- Request counters by path and status code
- Boot event log
- Embedded build and test output

## Documentation Page (`/docs/`)

Package documentation is generated at build time by `cmd/godochtml`, which:
- Walks the source tree using `go/parser` + `go/doc` (standard library, no external dependencies)
- Extracts exported types, functions, constants, variables, and their doc comments
- Renders package index with synopses, then per-package sections with type declarations and method signatures
- Outputs an HTML fragment that anyserver wraps in page chrome at startup

No JavaScript. Pure HTML+CSS, generated once during `build.sh`.

## API Reference Page (`/api/`)

OpenAPI specs from all services are merged and rendered to a static HTML page at build time by `cmd/swaggerhtml`. The tool:
- Parses multiple Swagger 2.0 JSON files
- Groups endpoints by service tag
- Renders parameters, response types, and schema definitions
- Outputs an HTML fragment that anyserver wraps in page chrome at startup

No JavaScript. The raw spec is also available at `/api/swagger.json`.

## Pages

| Path | Description |
|------|-------------|
| `/` | Index page with navigation links and README |
| `/source/` | Source code browser with directory listing, code view, media serving |
| `/docs/` | Package documentation (generated at build time from Go source via `go/doc` + `go/parser`) |
| `/api/` | API reference (static HTML rendered from OpenAPI specs at build time) |
| `/api/swagger.json` | Raw OpenAPI spec JSON |
| `/server/` | Server info: runtime stats, request counters, boot/build/test logs |
| `/gateway/` | Raw grpc-gateway REST proxy |

## HTTP Proxy Layer

Auto-generated godoc HTML is served **over HTTP only** (not via gRPC). The HTTP server handles `/docs/` paths using `docs.html` and `docs.css` templates.

When the HTTP proxy receives a `Source` stream from the gRPC service, it renders each `SourceCode` variant differently:

| SourceCode type | HTTP rendering |
|----------------|---------------|
| `Path` | Anchor tags (`<a href="/source/path/to/entry">`) with directory/file icons |
| `Code` | Code-block UI elements with language hint from filename extension (e.g., ` ```proto ... ``` ` style rendering with syntax highlighting) |
| `Media` | Appropriate media element for the MIME type (`<img>`, `<audio>`, `<video>`, etc.) with MIME metadata in headers |
| `Data` | Generic streamed response (chunked transfer / SSE for ongoing data) |
| `Binary` | Auto-download response (`Content-Disposition: attachment`) |

### HTTP response templating

Each gRPC service can provide an `HTTP.textproto` file containing templatized HTTP response mappings per response type. This is based on `HTTPResponse` from [accretional/httprpc](https://github.com/accretional/httprpc) (`proto/service.proto`):

```protobuf
// From httprpc — HTTPResponse envelope
message HTTPResponse {
  int32 status_code = 1;
  repeated HTTPHeader headers = 2;
  Body body = 3;
  map<string, google.protobuf.Value> decoded_data = 4;
}

// Streaming variant for SSE / chunked responses
message HTTPResponseChunk {
  oneof content {
    ResponseMetadata metadata = 1;
    bytes data_chunk = 2;
    google.protobuf.Struct json_chunk = 3;
    ResponseTrailers trailers = 4;
  }
}
```

If a service does not provide `HTTP.textproto`, the default behavior is to convert the gRPC response to JSON.

For streaming RPCs like `Source`, SSE (Server-Sent Events) may be used to push typed chunks to the browser, where `docs.html`/`docs.css` handle progressive rendering client-side.

The docs UI includes navigation: header/column linking to `/source/` URLs, project-level links, and breadcrumb navigation through the source tree.

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

Each injected service can optionally provide:
- `HTTP.textproto` — custom HTTP response templates for its gRPC response types
- `docs.html` / `docs.css` — custom rendering for its HTTP-browsable endpoints

If not provided, responses default to JSON serialization.

## Prior Art & Patterns Borrowed

### katarche
- `server.Run(grpcPort, httpPort, func(s *grpc.Server) { ... })` — callback-based service registration
- `tools/gen_go.sh` — three-phase codegen: rewrite proto `go_package` -> run protoc -> scan `*_grpc.pb.go` for `RegisterXyzServer()` and auto-generate main.go
- `tools/go_pull.sh` — import external protos into organized packages, create stub service dirs
- HTTP reflection UI via `DiscoverRPCs()` + dynamic form generation from proto descriptors
- gRPC reflection enabled for runtime introspection

### petros
- `registerServer(grpcServer)` — central function registering 9+ services at once
- Wrapper pattern for external services (CollectionServiceWrapper, etc.) to avoid method conflicts
- Custom HTTP-to-gRPC `/rpc-proxy/` bridge via reflection (same approach as katarche)
- Dual-port architecture (50051 gRPC, 3000 HTTP) with `PortManager` for conflict prevention

### gluon
- Go interface -> gRPC full codegen: `FullBootstrap(module, src)` pipeline
- AST toolkit (`astkit`) for type utilities, field ops, node builders, imports, function/struct helpers
- Proto compiler wrapper: runs `protoc` + `protoc-gen-go` + `protoc-gen-go-grpc`
- Round-trip verification: re-analyzes generated code to ensure structural integrity
- Direction: Go -> gRPC (opposite of what anyserver needs for external service import, but AST toolkit is reusable)

### godoc-gen
- CLI: `godoc-gen -output ./docs [-title "..."] [-single] [-preview] /path/to/repo1 /path/to/repo2`
- Generates static HTML via `go/parser` + `go/doc` AST parsing, styled like pkg.go.dev
- Single-file mode: self-contained HTML with embedded CSS/JS and package data as JSON
- Multi-file mode: `index.html` homepage + individual package pages under `pkg/`
- Built-in preview server on port 8090

### ffmpeg-proto / audio-visualizer
- `MediaConverter` gRPC service with 4 RPCs:
  - `conversion_stream` (bidirectional) — FFmpeg transforms on media chunks
  - `audio_to_vectors` (client-stream) — audio -> amplitude vectors (x=time, y=RMS 0-1)
  - `svg` (client-stream) — vectors -> SVG waveform visualization
  - `svg_to_sqlite` (unary) — SVG -> vectors for persistence
- 10 transform types via `oneof`: crop, scale, overlay, gif, slideshow, concat, mix, tempo, trim, insert
- Typed Go client in `pkg/client/` with high-level methods: `AudioWaveform()`, `AudioToVectors()`, `VectorsToSvg()`
- Session-based temp file management (UUID isolation under `/tmp/`)

### httprpc
- `HTTPResponse { status_code, headers, body, decoded_data }` — unified response envelope
- `HTTPResponseChunk { oneof { metadata, data_chunk, json_chunk, trailers } }` — streaming variant for SSE/chunked
- `DisplayService` — server-driven UI via `HTMLEncode` messages (backend controls DOM without JS frameworks)
- `HTTPEncode`/`HTTPDecode` — template-based request building and response extraction (JSON paths, CSS selectors, regex)
- UI component protos: `Table`, `Form`, `CodeBlock`, `Image` for server-side rendering
- `Body` oneof: raw bytes, JSON (`google.protobuf.Struct`), empty, or multipart

### mime-proto
- `MimeType { oneof type { DiscreteType discrete_media_type; string other_discrete_type; MultipartType multipart_type; } string sub_type; repeated MimeParameter params; }`
- `DiscreteType` enum: application, audio, example, font, image, model, text, video
- Used by `Media` messages in the Docs service to annotate embedded media files

## Plan / Next Steps

### Phase 1: Bootstrap anyserver
- [x] Set up Go module, `server/` package with dual gRPC/HTTP logic, `cmd/anyserver/main.go`, `services.go`
- [x] Define `docs.proto` with `Docs` service (`Source` streaming RPC with `SourceCode` oneof). Generate Go code + gateway + OpenAPI spec
- [x] Implement Docs service with `go:embed` repo source. Route by path type
- [x] Build HTTP source browser: directory listing with breadcrumbs, code view with line numbers, media serving
- [x] Wire grpc-gateway under `/gateway/` prefix. Serve OpenAPI spec at `/api/swagger.json`
- [x] Build index page with README rendering and navigation
- [x] Add `tools/gen.sh` for protoc codegen
- [x] Add `Metrics` gRPC service: Static (hostname, port, Go version, build/test/boot logs), Active (goroutines, heap, GC), Lifetime (uptime, request counters by path/status), Historical (TODO)
- [x] Add `/server/` page: pure HTML+CSS server info with runtime stats, request counters, boot/build/test logs
- [x] Add request counter middleware wrapping HTTP handler in server.go
- [x] Add `cmd/logpb` tool to capture build/test stdout as BuildLog/TestLog binarypb
- [x] Add `cmd/swaggerhtml` tool: merges OpenAPI specs into static HTML API reference page at build time
- [x] Render `/api/` from pre-generated HTML (no JavaScript, no Swagger UI)
- [x] Add `cmd/godochtml` tool: generates package documentation from Go source using `go/doc` + `go/parser`
- [x] Render `/docs/` from pre-generated HTML (no JavaScript, no external tools)
- [ ] **1d.** Build full HTTP proxy layer: `HTTP.textproto` mechanism based on httprpc's `HTTPResponse`/`HTTPResponseChunk`
- [ ] **1f.** Add `tools/gen.sh` support for auto-generating service registration by scanning `*_grpc.pb.go`

### Phase 2: Service composition / linking
- [ ] **2a.** Design service injection: `tools/gen.sh --inject /path/to/module` clones external module, discovers its `*_grpc.pb.go` files, extracts `RegisterXyzServer()` calls, auto-generates the wiring in `services.go`. Each injected service can optionally provide `HTTP.textproto` for custom HTTP rendering. Avoid petros's wrapper pattern where possible — prefer direct registration
- [ ] **2b.** Make `vad` export a clean registration entry point: `pkg/server/register.go` with `Register(s *grpc.Server, opts ...Option) error` that initializes ONNX Runtime, loads model, creates server, registers VoiceSegmentation
- [ ] **2c.** Update vad's Dockerfile to: clone anyserver -> `gen.sh --inject .` -> build unified binary containing both Docs and VoiceSegmentation services. Validate all existing vad tests still pass

### Phase 3: Docs and enhanced audio tooling
- [ ] **3a.** Extend godoc-gen if needed for anyserver integration (ensure output integrates with docs.html navigation, add cross-linking to `/source/` paths)
- [ ] **3b.** Integrate ffmpeg-proto as a composable service via the injection pattern: its `MediaConverter` service gets registered alongside Docs and VoiceSegmentation. Provide `HTTP.textproto` for waveform SVG rendering
- [ ] **3c.** Enhanced vad web UI: use ffmpeg-proto's `audio_to_vectors` + `svg` RPCs for server-side waveform visualization of uploaded audio. Add client-side download buttons for segmented chunks. Display SVG waveforms inline in results
- [ ] **3d.** Audio format handling: use ffmpeg-proto's `conversion_stream` for server-side decoding of any audio format to 16kHz PCM (currently client-side only via Web Audio API). This enables gRPC clients (not just browser) to send MP3/WAV directly

### Phase 4: Validation
- [ ] **4a.** anyserver builds and runs standalone with just the Docs service — source browsing works over both gRPC and HTTP, godoc HTML browsable at `/docs/`
- [ ] **4b.** vad builds with anyserver composition — all existing vad tests pass
- [ ] **4c.** ffmpeg-proto composes cleanly — waveform generation works end-to-end
- [ ] **4d.** Swagger UI shows all services, source is browsable with proper Code/Media/Binary rendering

## Related Projects

- [katarche](https://github.com/accretional/katarche) — unified gRPC service host with HTTP reflection UI (server pattern origin)
- [petros](https://github.com/accretional/petros) — multi-service gRPC host with custom HTTP proxy and wrapper pattern
- [gluon](https://github.com/accretional/gluon) — Go interface -> gRPC codegen, AST toolkit, proto compiler
- [godoc-gen](https://github.com/accretional/godoc-gen) — auto-generate godoc HTML from Go packages (CLI, static HTML)
- [ffmpeg-proto](https://github.com/accretional/ffmpeg-proto) — FFmpeg as a gRPC service (MediaConverter: conversion, waveforms, SVG)
- [audio-visualizer](https://github.com/accretional/audio-visualizer) — audio waveform visualization (same codebase as ffmpeg-proto)
- [httprpc](https://github.com/accretional/httprpc) — HTTP response/request protos, server-driven UI, template-based HTTP<->gRPC bridging
- [mime-proto](https://github.com/accretional/mime-proto) — MimeType protobuf definitions (used by Docs Media messages)
- [vad](https://github.com/accretional/vad) — pyannote VAD via ONNX (first consumer of anyserver)
