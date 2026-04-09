// Package anyserver provides a composable gRPC+HTTP server framework with
// built-in source browsing, API documentation, and server metrics.
package anyserver

import (
	"context"
	"html/template"
	"io/fs"
	"net/http"
	"strings"

	"github.com/accretional/anyserver/internal/docs"
	internalmetrics "github.com/accretional/anyserver/internal/metrics"
	appmetrics "github.com/accretional/anyserver/metrics"
	docspb "github.com/accretional/anyserver/proto/docs"
	metricspb "github.com/accretional/anyserver/proto/metrics"
	"github.com/accretional/anyserver/server"
	"github.com/accretional/anyserver/wormhole"
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

	// APIHTML is pre-generated API reference HTML fragment (from swaggerhtml tool).
	APIHTML []byte

	// DocsHTML is pre-generated package documentation HTML fragment (from godochtml tool).
	DocsHTML []byte

	// BuildLogPB is the serialized BuildLog proto (optional, from build.binarypb).
	BuildLogPB []byte

	// TestLogPB is the serialized TestLog proto (optional, from tests.binarypb).
	TestLogPB []byte

	// ReadmeHTML is pre-rendered README content for the index page.
	ReadmeHTML template.HTML

	// ExtraGRPC allows registering additional gRPC services.
	ExtraGRPC []func(s *grpc.Server)

	// ExtraGateway allows registering additional grpc-gateway handlers.
	ExtraGateway []server.GatewayRegisterFunc

	// ExtraHTTP allows mounting additional HTTP routes.
	ExtraHTTP func(mux *http.ServeMux)

	// Wormholes is the registry of named streams. If set, a /wormhole/
	// endpoint is mounted and the requests wormhole is created automatically.
	Wormholes *wormhole.Registry
}

// Run starts the anyserver with the given configuration.
func Run(cfg Config) error {
	docsSvc := docs.New(cfg.SourceFS)
	counter := appmetrics.NewRequestCounter()
	metricsSvc := internalmetrics.New(cfg.Port, counter, cfg.BuildLogPB, cfg.TestLogPB)

	metricsSvc.RecordBootStarted()

	httpMux := http.NewServeMux()

	// Mount source browsing
	docsHandler := docs.HTTPHandler(cfg.SourceFS, cfg.DocsFS, cfg.RepoName, cfg.ReadmeHTML)
	httpMux.Handle("/source/", docsHandler)

	// Mount docs (pre-generated HTML or godoc FS or placeholder)
	if len(cfg.DocsHTML) > 0 {
		docsPage := renderDocsPage(cfg.RepoName, template.HTML(cfg.DocsHTML))
		httpMux.HandleFunc("/docs/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/docs/" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(docsPage)
		})
	} else if cfg.DocsFS != nil {
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
	}
	if len(cfg.APIHTML) > 0 {
		apiPage := renderAPIPage(cfg.RepoName, template.HTML(cfg.APIHTML))
		httpMux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(apiPage)
		})
	} else {
		httpMux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
			servePlaceholder(w, cfg.RepoName, "API",
				"No OpenAPI spec available. Run <code>tools/gen.sh</code> to generate.")
		})
	}

	// Mount server/metrics page
	httpMux.Handle("/server/", metricsSvc.HTTPHandler(cfg.RepoName))

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

	// Wormhole streams
	if cfg.Wormholes != nil {
		// Create and register requests wormhole
		reqWH := wormhole.New(wormhole.KindRequests, "Live HTTP request log")
		cfg.Wormholes.Register(reqWH)
		counter.SetStream(reqWH)

		// Create boot wormhole
		bootWH := wormhole.New(wormhole.KindBoot, "Boot lifecycle events")
		cfg.Wormholes.Register(bootWH)

		httpMux.Handle("/wormhole/", wormhole.HTTPHandler(cfg.Wormholes, cfg.RepoName))
	}

	// Gateway registrations
	gateways := []server.GatewayRegisterFunc{
		func(ctx context.Context, mux *runtime.ServeMux, conn *grpc.ClientConn) error {
			return docspb.RegisterDocsHandlerClient(ctx, mux, docspb.NewDocsClient(conn))
		},
		func(ctx context.Context, mux *runtime.ServeMux, conn *grpc.ClientConn) error {
			return metricspb.RegisterMetricsHandlerClient(ctx, mux, metricspb.NewMetricsClient(conn))
		},
	}
	gateways = append(gateways, cfg.ExtraGateway...)

	metricsSvc.RecordBootComplete()

	return server.Run(server.Config{
		Port: cfg.Port,
		GRPCRegister: func(s *grpc.Server) {
			docspb.RegisterDocsServer(s, docsSvc)
			metricspb.RegisterMetricsServer(s, metricsSvc)
			for _, reg := range cfg.ExtraGRPC {
				reg(s)
			}
		},
		GatewayRegisters: gateways,
		HTTPMux:          httpMux,
		RequestCounter:   counter,
	})
}

func serveIndex(w http.ResponseWriter, cfg Config) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl := template.Must(template.New("index").Parse(indexTemplate))
	tmpl.Execute(w, struct {
		RepoName   string
		ReadmeHTML template.HTML
	}{cfg.RepoName, cfg.ReadmeHTML})
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

func renderAPIPage(repoName string, content template.HTML) []byte {
	tmpl := template.Must(template.New("api").Parse(apiPageTemplate))
	var buf strings.Builder
	tmpl.Execute(&buf, struct {
		RepoName string
		Content  template.HTML
	}{repoName, content})
	return []byte(buf.String())
}

func renderDocsPage(repoName string, content template.HTML) []byte {
	tmpl := template.Must(template.New("docs").Parse(docsPageTemplate))
	var buf strings.Builder
	tmpl.Execute(&buf, struct {
		RepoName string
		Content  template.HTML
	}{repoName, content})
	return []byte(buf.String())
}

const navHTML = `<header class="header">
  <a href="/" class="header-title">{{.RepoName}}</a>
  <nav class="header-nav">
    <a href="/source/">Source</a>
    <a href="/docs/">Docs</a>
    <a href="/api/">API</a>
    <a href="/server/">Server</a>
    <a href="/wormhole/">Wormhole</a>
  </nav>
</header>`

const indexTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{{.RepoName}}</title>
<link rel="stylesheet" href="/static/docs.css">
</head>
<body>
` + navHTML + `
<main class="content">
  <section class="index-section">
    <h2>Navigation</h2>
    <div class="index-links">
      <a href="/source/">Browse Source</a>
      <a href="/docs/">Documentation</a>
      <a href="/api/">API (OpenAPI)</a>
      <a href="/server/">Server Info</a>
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
` + navHTML + `
<main class="content">
  <section class="index-section">
    <h2>{{.Section}}</h2>
    <p>{{.Message}}</p>
  </section>
</main>
</body>
</html>`

const docsPageTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Docs - {{.RepoName}}</title>
<link rel="stylesheet" href="/static/docs.css">
</head>
<body>
` + navHTML + `
<main class="content">
{{.Content}}
</main>
</body>
</html>`

const apiPageTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>API - {{.RepoName}}</title>
<link rel="stylesheet" href="/static/docs.css">
</head>
<body>
` + navHTML + `
<main class="content">
{{.Content}}
</main>
</body>
</html>`
