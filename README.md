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

## Plan / Next Steps

### Phase 1: Bootstrap anyserver
- [x] Create repo
- [ ] **1a.** Set up Go module, `server/` package with dual gRPC/HTTP logic (borrow katarche's `server.Run()` callback pattern), `cmd/anyserver/main.go`, `services.go`
- [ ] **1b.** Define `docs.proto` with `Docs` service (GetSource, ListSource, HTML). Generate Go code
- [ ] **1c.** Implement Docs service: `go:embed` repo source, serve via GetSource/ListSource. HTML returns placeholder initially
- [ ] **1d.** Integrate grpc-gateway: add HTTP annotations to `docs.proto`, generate gateway proxy + OpenAPI spec, wire into HTTP server
- [ ] **1e.** Build index.html: rendered README.md, Swagger UI, repo name as title, metadata links. Add style.css
- [ ] **1f.** Add `tools/gen.sh`: runs protoc with go/grpc/gateway/openapi plugins, optionally auto-generates service registration by scanning `*_grpc.pb.go`

### Phase 2: Service composition / linking
- [ ] **2a.** Design service injection pattern: external modules export `Register(*grpc.Server)`, gen.sh discovers and wires them
- [ ] **2b.** Make `vad` export a clean `Register()` entry point
- [ ] **2c.** Update vad's Dockerfile to clone anyserver, inject vad's service, build — validate composition works

### Phase 3: Docs and enhanced tooling
- [ ] **3a.** Build/extend `godoc-gen` to produce static HTML from Go packages. Wire into Docs service's `HTML()` RPC
- [ ] **3b.** Define pattern for integrating `ffmpeg-proto` and `audio-visualizer` as composable services
- [ ] **3c.** Enhanced client-side audio management in basic-vad-web

### Phase 4: Validation
- [ ] **4a.** Full test validation: anyserver builds standalone, vad builds with anyserver composition, all existing tests pass, Swagger UI and docs accessible

## Related Projects

- [katarche](https://github.com/accretional/katarche) — unified gRPC service host with HTTP reflection UI (server pattern origin)
- [gluon](https://github.com/accretional/gluon) — Go interface → gRPC codegen, AST toolkit, proto compiler
- [godoc-gen](https://github.com/accretional/godoc-gen) — auto-generate godoc HTML from Go packages
- [ffmpeg-proto](https://github.com/accretional/ffmpeg-proto) — FFmpeg as a gRPC service
- [audio-visualizer](https://github.com/accretional/audio-visualizer) — audio visualization service
- [vad](https://github.com/accretional/vad) — pyannote VAD via ONNX (first consumer of anyserver)
