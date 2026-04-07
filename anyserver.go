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

	// SwaggerJSON is the raw OpenAPI spec JSON (optional).
	SwaggerJSON []byte

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

	// Mount source browsing
	docsHandler := docs.HTTPHandler(cfg.SourceFS, cfg.DocsFS, cfg.RepoName, cfg.ReadmeHTML)
	httpMux.Handle("/source/", docsHandler)

	// Mount docs (godoc HTML or placeholder)
	if cfg.DocsFS != nil {
		httpMux.Handle("/docs/", docsHandler)
	} else {
		httpMux.HandleFunc("/docs/", func(w http.ResponseWriter, r *http.Request) {
			servePlaceholder(w, cfg.RepoName, "Documentation",
				"Documentation has not been generated yet. Run <code>godoc-gen</code> to generate.")
		})
	}

	// Mount API / swagger
	if len(cfg.SwaggerJSON) > 0 {
		httpMux.HandleFunc("/api/swagger.json", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write(cfg.SwaggerJSON)
		})
		httpMux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/" {
				http.NotFound(w, r)
				return
			}
			serveSwaggerPage(w, cfg.RepoName)
		})
	} else {
		httpMux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
			servePlaceholder(w, cfg.RepoName, "API",
				"No OpenAPI spec available. Run <code>tools/gen.sh</code> to generate.")
		})
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

func servePlaceholder(w http.ResponseWriter, repoName, section, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl := template.Must(template.New("placeholder").Parse(placeholderTemplate))
	tmpl.Execute(w, struct {
		RepoName string
		Section  string
		Message  template.HTML
	}{repoName, section, template.HTML(message)})
}

func serveSwaggerPage(w http.ResponseWriter, repoName string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl := template.Must(template.New("swagger").Parse(swaggerTemplate))
	tmpl.Execute(w, struct{ RepoName string }{repoName})
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
      <a href="/api/">API (OpenAPI)</a>
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

const placeholderTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{{.Section}} - {{.RepoName}}</title>
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
    <h2>{{.Section}}</h2>
    <p>{{.Message}}</p>
  </section>
</main>
</body>
</html>`

const swaggerTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>API - {{.RepoName}}</title>
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
    <h2>API</h2>
    <p>OpenAPI specification: <a href="/api/swagger.json">/api/swagger.json</a></p>
    <h3>Docs Service</h3>
    <table class="file-list">
      <thead><tr><th>Method</th><th>Path</th><th>Description</th></tr></thead>
      <tbody>
        <tr>
          <td>GET</td>
          <td><code>/source/{path}</code></td>
          <td>Browse embedded source. Returns directory listings, code, media, or binary.</td>
        </tr>
      </tbody>
    </table>
    <h3>gRPC</h3>
    <p>gRPC reflection is enabled. Connect with any gRPC client on the same port.</p>
  </section>
</main>
</body>
</html>`
