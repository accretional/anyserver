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
services.go              <- declarative list of gRPC services to include (consumed by tools/gen.sh — see Plan 1f)
anyserver.go             <- library entry point: Config + Run() wires all services
server/
  server.go              <- dual gRPC/HTTP server on a single h2c port; grpc-gateway, request counter, boot gate
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
proto/files/
  server.proto           <- Files service: Get(path) -> stream Resource (defined; impl pending — see Plan)
  resource.proto         <- Resource oneof: document/media/data/socket
  document.proto media.proto data.proto socket.proto
  *.pb.go *.pb.gw.go *.swagger.json
internal/docs/
  service.go             <- Docs service implementation (go:embed source)
  httphandler.go         <- /source/{tree.json,raw/<path>,/} JSON+text endpoints used by the sidebar tree + file viewer
internal/metrics/
  service.go             <- Metrics service implementation (runtime stats, request counters, build/test/boot logs)
  httphandler.go         <- /server/ page (shared chrome via internal/ui; iframe panes for /wormhole/{requests,stdout,stderr})
internal/ui/
  nav.go                 <- shared top-of-page ticker used by /docs, /api, /server, placeholders
  footer.go              <- shared all-footer (expandable frunk, menu collapse, links — CSS-only)
metrics/
  request_counter.go     <- HTTP middleware tracking requests by path and status code (streams to /wormhole/requests)
  procfs.go              <- Go runtime stats (goroutines, heap, GC); TODO: Linux procfs
wormhole/
  wormhole.go            <- Kind/Wormhole types: named, fan-out broadcast streams
  registry.go            <- in-process registry of named wormholes
  capture.go             <- redirects os.Stdout/os.Stderr into KindStdout/KindStderr wormholes
  command.go             <- token-authenticated KindCommand channel + boot-time handshake
  command_handler.go     <- WebSocket protocol for the command channel
  httphandler.go         <- /wormhole/ WS + /wormhole/{kind}/pane iframe HTML
cmd/swaggerhtml/
  main.go                <- build tool: merges OpenAPI specs into static HTML reference page
cmd/godochtml/
  main.go                <- build tool: generates /docs/ HTML from Go source using go/doc + go/parser
cmd/logpb/
  main.go                <- build tool: serializes stdout to BuildLog/TestLog binarypb
static/
  base.css               <- variables, panel/tab primitives, all-footer, scrollbar, link styling
  app.css                <- root index page only: sidebar layout, dock, multi-panel main
  docs.css               <- /docs, /api, /server, placeholder page styling
  accretion_256.webp     <- spinner-disk asset in the footer
  cubist_thin_full.svg   <- sidebar decoration (rendered via CSS mask in amber)
  plato_11_isometric.svg <- alternate sidebar decoration
chrome-testing/
  snap.sh                <- vendored from accretional/chromerpc; URL -> PNG via headless Chrome
  snapshots/             <- committed PNGs for visual progress tracking
snap.sh                  <- top-level wrapper: snaps /, /#terms, /docs, /api, /server against a running server
LET_IT_RIP.sh            <- non-blocking pipeline: setup + test + build + serve (nohup) + snap + open
tools/
  gen.sh                 <- protoc + gateway + openapi codegen (auto service registration: see Plan 1f)
```

## Quick Start

```bash
./setup.sh        # install protoc plugins, download third-party protos
./tools/gen.sh    # regenerate proto Go code, gateway, OpenAPI specs
./build.sh        # embed source + static assets, generate API/docs HTML, build binary
./test.sh         # vet, test, build, smoke test (validates all endpoints)
./snap.sh         # screenshot /, /#terms, /docs, /api, /server via headless Chrome
./LET_IT_RIP.sh   # full pipeline: setup + test + build + serve + snap + open browser
```

`LET_IT_RIP.sh` is **non-blocking** — it detaches the server via `nohup` and exits as soon as snap + browser-open succeed. Re-invoking it kills the prior server on the port and rebuilds fresh. PNGs land in `chrome-testing/snapshots/` and are committed alongside code so you can review UI progress in the repo history.

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
    PathEntry path = 1;  // Directory listing entry (file or subdirectory)
    Code code = 2;       // Source code / text file chunk
    Media media = 3;     // Image, audio, video, etc.
    Data data = 4;       // Generic streamed data (large files, mixed content)
    Binary binary = 5;   // Opaque binary (triggers download over HTTP)
  }
}

message PathEntry {
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
    bool more = 4;         // More chunks follow
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

## Service Mesh UI (`cmd/meshadapter` + `cmd/meshserver`)

The same `/api/` page can front a *live* service mesh instead of a single
build-time spec. Two commands make this work:

- **`cmd/meshadapter`** — turns the mesh's service definitions into a single
  merged Swagger 2.0 spec. It ingests two kinds of input (the same currency the
  KVQ TypeRegistry speaks):
  1. **Live gRPC server reflection** from running backends (e.g. chromerpc on
     `:50051`). Every reflection-enabled backend contributes its services and
     types to the one UI; these tags are marked `LIVE via gRPC reflection`.
  2. **Curated protobuf FileDescriptorSets** (`.pb`, `protoc --include_imports`
     output). Kept to a SMALL curated set to stay within memory limits.

  Each gRPC service becomes a Swagger tag, each method a `POST /<pkg.Service>/<Method>`
  path, and each message a definition. Output: a merged `swagger.json`.

- **`cmd/meshserver`** — unlike `cmd/anyserver` (which `go:embed`s its own
  spec at build time), meshserver reads an externally-generated `swagger.json`
  and `api.html` at startup and serves them via `anyserver.Run`. The result:
  one `/api/` page presenting EVERY integrated service/type in the mesh.

End-to-end pipeline (reflection + curated sets -> merged spec -> rendered HTML
-> served UI) is captured in **`cmd/meshserver/meshui.sh`**:

```bash
# fronts live chromerpc (:50051) + a curated SMALL descriptor set, serves on :8093
PORT=8093 ./cmd/meshserver/meshui.sh
```

Validate through chromerpc (macOS has no `timeout`; use grpcurl's `-max-time`):

```bash
grpcurl -max-time 20 -plaintext -d '{"url":"http://localhost:8093/api/"}' \
  localhost:50051 cdp.page.PageService/Navigate
sleep 2
grpcurl -max-time 20 -plaintext -d '{"format":"SCREENSHOT_FORMAT_PNG"}' \
  localhost:50051 cdp.page.PageService/CaptureScreenshot \
  | python3 -c "import sys,json,base64; open('/tmp/anyserver-mesh-ui.png','wb').write(base64.b64decode(json.load(sys.stdin)['data']))"
```

**Memory discipline:** only load the curated SMALL descriptor sets (e.g.
`macos-vision.pb`, `vad.pb`, `oss-aether.pb`, `proto-ip.pb`). Do NOT load the
giant sets (datadog/digitalocean/stripe/github/openrouter).

## Pages

| Path | Description |
|------|-------------|
| `/` | Interactive root page: sidebar (filetype-tagged source tree), multi-panel main, bottom dock, expandable footer |
| `/source/` | Source browser (HTML page; routes to `/source/tree.json` + `/source/raw/<path>` for data) |
| `/source/tree.json` | JSON tree of the embedded source FS (used by the sidebar tree) |
| `/source/raw/<path>` | Raw text contents of an embedded source file (used by the file viewer) |
| `/docs/` | Package documentation (generated at build time from Go source via `go/doc` + `go/parser`) |
| `/api/` | API reference (static HTML rendered from OpenAPI specs at build time) |
| `/api/swagger.json` | Raw OpenAPI spec JSON |
| `/server/` | Server info: runtime stats, request counters, boot/build/test logs, live wormhole panes |
| `/wormhole/{kind}` | WebSocket stream of a named wormhole (`stdout`, `stderr`, `requests`, `boot`, `command`) |
| `/wormhole/{kind}/pane` | Iframe HTML page that connects to `/wormhole/{kind}` and renders into a `<pre>` |
| `/files/{path=**}` | Files service (proto defined in `proto/files/`; service implementation pending — see Plan) |
| `/gateway/` | Raw grpc-gateway REST proxy |
| `/static/<file>` | Embedded static assets (CSS, SVG, webp) |

## Files Service

The `Files` service (`proto/files/server.proto`) defines a single streaming RPC:

```protobuf
service Files {
  rpc Get(GetRequest) returns (stream Resource) {
    option (google.api.http) = { get: "/files/{path=**}" };
  }
}
```

A `Resource` is a `oneof` over `Document`, `Media`, `Data`, `Socket` — the typed-content companion to `Docs.Source` for user-uploaded / non-embedded files. **The proto is defined and codegen runs; the server-side implementation is pending** (see Plan). When implemented, it will be the consumer-facing read API for arbitrary file content (vs. `Docs.Source`, which is scoped to the repo's own embedded sources).

## Wormhole Streams

Anyserver provides named, in-process **wormhole** streams — fan-out broadcast pipes that any handler can write to and any HTTP/WebSocket client can subscribe to. Five kinds ship out of the box:

| Kind | Source | Wire-up |
|------|--------|---------|
| `stdout` | `os.Stdout` redirected at boot | `wormhole.CaptureOutputs` |
| `stderr` | `os.Stderr` redirected at boot | `wormhole.CaptureOutputs` |
| `requests` | every HTTP request, after the response is written | `metrics.RequestCounter.SetStream` |
| `boot` | `BOOT_STARTED` / `BOOT_COMPLETE` events | `metrics.RecordBoot*` |
| `command` | authenticated command channel — opt-in | `wormhole.NewCommandWormhole` |

HTTP routes:
- `GET /wormhole/{kind}` — WebSocket upgrade; server pushes appended chunks to the client.
- `GET /wormhole/{kind}/pane` — self-contained iframe HTML that connects to the WS and renders into a `<pre>`. Used by the `/server/` page and the root dock to embed live tails.
- `GET /wormhole/{kind}?tail=N` — non-WS one-shot returning the last N entries (used by smoke tests).

The **command wormhole** is special: when enabled (`Config.CommandWormhole.Enabled`), anyserver prints a one-time `COMMAND TOKEN: …` to stderr at boot, then holds a `server.BootGate` closed until a client posts the token to `/wormhole/command`. Until the gate opens, only the index, /source, /docs, /api, /server, /static, and /wormhole paths are served; everything else returns 503. This is the hook the root-page dock uses to enable the interactive COMMAND tab.

## Web UI

The root page (`/`) is a single interactive shell, rendered server-side from `html/template` in `anyserver.go`. Layout:

- **Sidebar** — repo name + Documentation/API Reference/Server Info links + amber-masked SVG decoration + filetype-tagged file tree (JS fetches `/source/tree.json`, click loads via `/source/raw/<path>` into the file viewer)
- **Main area** — three side-by-side panels: WORLDLY iframe (external 3D map), DOCUMENTATION iframe (`/docs/`), file viewer (CSS-counter line numbers)
- **Dock** — collapsed to a 0-height toggle nub by default; expanded shows the wormhole console tabs (COMMAND / REQUESTS / STDOUT / STDERR — each an iframe to `/wormhole/{kind}/pane`) plus HOST/NETWORK, SERVER & APPLICATION, and SERVICE info panels
- **Footer** — shared across every page via `internal/ui.Footer`. Always-visible base strip with status + a 6-link menu (About / Privacy / Terms / API / Docs / Accretional) and a `>>`/`<<` collapse toggle. Clicking any link opens the **frunk** below the base: a per-`<p>` content panel selected by `:target`, with a contextual submenu and a `×` close anchor.

The top ticker (Home · Source · Docs · API · Server) shared by `/docs`, `/api`, `/server`, and placeholder pages lives in `internal/ui.Nav`. Same constant, every page.

**Interactivity is CSS-only where possible:** the footer's expand/collapse, frunk activation, submenu switching, and menu collapse use the `:target` selector, `:has()`, and the checkbox-label trick — no JavaScript. JS only appears for things genuinely dynamic (WebSocket wormhole tails, file-tree loading, dock toggle).

## Visual Regression (chrome-testing + snap)

`./snap.sh` boots a headless Chrome via [chromerpc](https://github.com/accretional/chromerpc) and screenshots a running anyserver. Captured pages: `/`, `/#terms` (verifies the frunk pops under the footer), `/docs/`, `/api/`, `/server/`. PNGs land in `chrome-testing/snapshots/` and **are committed** so the repo history shows UI evolution alongside code.

`chrome-testing/` is vendored from `accretional/chromerpc/chrome-testing/` — its own `snap.sh` is the per-URL primitive; the top-level `snap.sh` is an anyserver-specific multi-URL wrapper. `LET_IT_RIP.sh` calls the top-level script as a stage between smoke tests and browser-open.

## HTTP Proxy Layer

_(Planned — see Phase 1d. The mechanism described below is the design target; today, gRPC services that need custom HTTP rendering hook in via `Config.ExtraHTTP`.)_

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

_(The `tools/gen.sh --inject` codegen flow below is planned — see Phase 1f / 2a. **Today**, composition is by Go API: a caller passes service registration functions through `anyserver.Config`'s `ExtraGRPC []func(*grpc.Server)`, `ExtraGateway []server.GatewayRegisterFunc`, and `ExtraHTTP func(*http.ServeMux)` fields. The codegen layer described below is the future ergonomic — direct injection works today.)_

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
- [x] Add `wormhole/` package: named fan-out streams (`stdout`, `stderr`, `requests`, `boot`), WebSocket + iframe-pane HTTP routes, request-counter middleware writes to `requests` wormhole, stdout/stderr captured via `wormhole.CaptureOutputs` at boot
- [x] Add token-authenticated `command` wormhole + `server.BootGate`: server holds non-allowlisted routes at 503 until a client posts the boot token, enabling secure interactive command sessions from the dock
- [x] Build interactive root page: sidebar (filetype-tagged source tree, plato/cubist SVG decoration), multi-panel main (Worldly iframe / `/docs` iframe / file viewer), bottom dock with live wormhole consoles + host/server/service info panels
- [x] Add shared `internal/ui` package: `Nav` (top ticker) and `Footer` (expandable all-footer with frunk) used by every page — CSS-only via `:target` + `:has()` + checkbox-label trick
- [x] Site-wide amber theme: `scrollbar-color` on `html`, `.iframe-pane` class for embedded iframes, `a[target="_blank"]::after { content: '↗' }`; wormhole pane templates load `/static/base.css` so iframe scrollbars inherit
- [x] Add `chrome-testing/` (vendored from `accretional/chromerpc`) + top-level `snap.sh` wrapper that captures `/`, `/#terms`, `/docs`, `/api`, `/server`. Snapshots committed to `chrome-testing/snapshots/` for visual progress tracking
- [x] Make `LET_IT_RIP.sh` non-blocking: `nohup`-detach the server, exit after snap, `lsof -sTCP:LISTEN` so re-runs don't kill browser client connections
- [x] Define `Files` proto service (`proto/files/server.proto` + Resource/Document/Media/Data/Socket sub-protos) and run codegen
- [ ] **1c.** Implement `Files` service: server-side `Get(path) -> stream Resource` for arbitrary (non-embedded) file content, mounted at `/files/{path=**}`
- [ ] **1d.** Build full HTTP proxy layer: `HTTP.textproto` mechanism based on httprpc's `HTTPResponse`/`HTTPResponseChunk`
- [ ] **1f.** Add `tools/gen.sh` support for auto-generating service registration by scanning `*_grpc.pb.go` (today: `services.go` is a declarative list, but `gen.sh` doesn't consume it yet — composition is direct via `Config.ExtraGRPC` etc.)

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
