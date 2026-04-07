package anyserver

import (
	"context"
	"html/template"
	"io/fs"
	"net/http"

	"github.com/accretional/anyserver/internal/docs"
	pb "github.com/accretional/anyserver/proto/docs"
	"github.com/accretional/anyserver/server"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
)

// Config holds the configuration for the anyserver instance.
type Config struct {
	// Port to listen on.
	Port int

	// RepoName is used as the page title/header.
	RepoName string

	// SourceFS is the filesystem containing embedded source code.
	SourceFS fs.FS

	// DocsFS is the filesystem containing generated godoc HTML (optional).
	DocsFS fs.FS

	// StaticFS is the filesystem containing static assets (css, etc).
	StaticFS fs.FS

	// ReadmeHTML is pre-rendered README content for the index page.
	ReadmeHTML template.HTML

	// ExtraGRPC allows registering additional gRPC services.
	ExtraGRPC []func(s *grpc.Server)

	// ExtraGateway allows registering additional grpc-gateway handlers.
	ExtraGateway []server.GatewayRegisterFunc

	// ExtraHTTP allows mounting additional HTTP routes.
	ExtraHTTP func(mux *http.ServeMux)
}

// Run starts the anyserver with the given configuration.
func Run(cfg Config) error {
	docsSvc := docs.New(cfg.SourceFS)

	httpMux := http.NewServeMux()

	// Mount source browsing and docs
	docsHandler := docs.HTTPHandler(cfg.SourceFS, cfg.DocsFS, cfg.RepoName, cfg.ReadmeHTML)
	httpMux.Handle("/source/", docsHandler)
	if cfg.DocsFS != nil {
		httpMux.Handle("/docs/", docsHandler)
	}

	// Serve static assets
	if cfg.StaticFS != nil {
		httpMux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(cfg.StaticFS))))
	}

	// Index page
	httpMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		serveIndex(w, cfg)
	})

	// Extra HTTP routes
	if cfg.ExtraHTTP != nil {
		cfg.ExtraHTTP(httpMux)
	}

	// Gateway registrations
	gateways := []server.GatewayRegisterFunc{
		func(ctx context.Context, mux *runtime.ServeMux, conn *grpc.ClientConn) error {
			return pb.RegisterDocsHandlerClient(ctx, mux, pb.NewDocsClient(conn))
		},
	}
	gateways = append(gateways, cfg.ExtraGateway...)

	return server.Run(server.Config{
		Port: cfg.Port,
		GRPCRegister: func(s *grpc.Server) {
			pb.RegisterDocsServer(s, docsSvc)
			for _, reg := range cfg.ExtraGRPC {
				reg(s)
			}
		},
		GatewayRegisters: gateways,
		HTTPMux:          httpMux,
	})
}

func serveIndex(w http.ResponseWriter, cfg Config) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	tmpl := template.Must(template.New("index").Parse(indexTemplate))
	tmpl.Execute(w, struct {
		RepoName   string
		ReadmeHTML template.HTML
	}{
		RepoName:   cfg.RepoName,
		ReadmeHTML: cfg.ReadmeHTML,
	})
}

const indexTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{{.RepoName}}</title>
<link rel="stylesheet" href="/static/docs.css">
</head>
<body>
<header class="header">
  <a href="/" class="header-title">{{.RepoName}}</a>
  <nav class="header-nav">
    <a href="/source/">Source</a>
    <a href="/docs/">Docs</a>
    <a href="/api/">API</a>
  </nav>
</header>
<main class="content">
  <section class="index-section">
    <h2>Navigation</h2>
    <div class="index-links">
      <a href="/source/">Browse Source</a>
      <a href="/docs/">Documentation</a>
      <a href="/api/">API (Swagger)</a>
    </div>
  </section>
  {{if .ReadmeHTML}}
  <section class="index-section">
    <h2>README</h2>
    <div class="readme-content">{{.ReadmeHTML}}</div>
  </section>
  {{end}}
</main>
</body>
</html>`
