// Package anyserver provides a composable gRPC+HTTP server framework with
// built-in source browsing, API documentation, and server metrics.
package anyserver

import (
	"context"
	"fmt"
	"html/template"
	"io/fs"
	"log"
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

	// CommandWormhole configures the secure command channel. If nil or
	// Enabled is false, no command wormhole is created and boot does not pause.
	CommandWormhole *wormhole.CommandConfig
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

	// Set up command wormhole and boot gate if enabled
	var gate *server.BootGate
	var commandWH *wormhole.CommandWormhole
	var listening chan struct{}

	if cfg.CommandWormhole != nil && cfg.CommandWormhole.Enabled && cfg.Wormholes != nil {
		var err error
		commandWH, err = wormhole.NewCommandWormhole(*cfg.CommandWormhole)
		if err != nil {
			return fmt.Errorf("command wormhole: %w", err)
		}

		cfg.Wormholes.Command = commandWH
		cfg.Wormholes.RegisterHidden(commandWH.Wormhole())

		gate = &server.BootGate{
			Ready: make(chan struct{}),
			AllowPath: func(path string) bool {
				return strings.HasPrefix(path, "/wormhole/") ||
					path == "/" ||
					strings.HasPrefix(path, "/source/") ||
					strings.HasPrefix(path, "/docs/") ||
					strings.HasPrefix(path, "/api/") ||
					path == "/server/" ||
					strings.HasPrefix(path, "/static/")
			},
		}
		listening = make(chan struct{})
	}

	// Without command wormhole, boot completes immediately
	if commandWH == nil {
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

	// With command wormhole: start server in goroutine, wait for auth
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Run(server.Config{
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
			Gate:             gate,
			Listening:        listening,
		})
	}()

	// Wait for listener to be ready
	select {
	case <-listening:
	case err := <-serverErr:
		return err
	}

	// Block on auth handshake
	if err := commandWH.WaitForAuth(); err != nil {
		log.Printf("command wormhole: %v, proceeding without command session", err)
	}

	// Open the gate
	close(gate.Ready)
	metricsSvc.RecordBootComplete()

	// Block on server
	return <-serverErr
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

const navHTML = `<div class="ticker">
  <span><b>{{.RepoName}}</b></span><span class="dot">·</span>
  <span><a href="/">Home</a></span><span class="dot">·</span>
  <span><a href="/source/">Source</a></span><span class="dot">·</span>
  <span><a href="/docs/">Docs</a></span><span class="dot">·</span>
  <span><a href="/api/">API</a></span><span class="dot">·</span>
  <span><a href="/server/">Server</a></span>
</div>`

const indexTemplate = `<!DOCTYPE html>
<html lang="en"><head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{{.RepoName}}</title>
<style>
:root{--amber:#f59e0b;--amber-dim:rgba(245,158,11,0.55);--amber-bg:rgba(245,158,11,0.07);--stroke:rgba(245,158,11,0.35);--text:#d6d6d6;--muted:#8a8a8a}
*{box-sizing:border-box}
html,body{height:100%}
body{margin:0;background:#000;color:var(--text);font-family:"SF Mono",ui-monospace,Menlo,Consolas,monospace;font-size:11px;line-height:1.35;-webkit-font-smoothing:antialiased;overflow:hidden;display:flex;flex-direction:column}
.ticker{position:relative;height:24px;display:flex;align-items:center;gap:10px;padding:0 8px;background:rgba(0,0,0,.86);border-bottom:1px solid var(--amber);backdrop-filter:blur(6px);z-index:60;white-space:nowrap;overflow:hidden;flex-shrink:0}
.ticker a{color:var(--amber);text-decoration:none}
.ticker a:hover{text-decoration:underline}
.ticker .dot{color:var(--amber);opacity:.7}
.ticker b{color:var(--amber);font-weight:600}
.main-area{flex:1;display:flex;overflow:hidden;position:relative;z-index:1}
.sidebar{width:240px;flex-shrink:0;background:rgba(5,5,5,.9);border-right:1px solid var(--stroke);display:flex;flex-direction:column;overflow:hidden}
.sidebar .panel{border:none;border-bottom:1px solid var(--stroke);background:transparent;flex-shrink:0}
.sidebar .panel.tree-panel{border-bottom:none;flex:1;min-height:0;display:flex;flex-direction:column}
.sidebar .panel.search-panel{flex-shrink:0;border-top:1px solid var(--stroke);border-bottom:none}
.sidebar .panel-head{padding:4px 8px;background:var(--amber-bg);color:var(--amber);text-transform:uppercase;letter-spacing:.08em;font-weight:600;font-size:10px;border-bottom:1px solid var(--stroke)}
.sidebar .search{width:100%;background:#000;color:var(--text);border:none;padding:6px 8px;font-family:inherit;font-size:11px;outline:none}
.sidebar .search::placeholder{color:var(--muted)}
.tree{list-style:none;padding:4px 8px;overflow-y:auto;overflow-x:hidden;flex:1;font-size:11px;line-height:1.6}
.tree li{white-space:nowrap;overflow:hidden;text-overflow:ellipsis;cursor:pointer;padding:0 2px}
.tree li:hover{background:rgba(245,158,11,.08)}
.tree li .sz{color:var(--muted);margin-left:4px;font-size:10px}
.tree li .cnt{color:var(--muted);margin-left:4px;font-size:10px}
.tree li a{color:var(--text);text-decoration:none}
.tree li a:hover{color:var(--amber)}
.tree li.dir>a{color:var(--amber)}
.tree .dot{display:inline-block;width:6px;height:6px;border-radius:50%;margin-right:4px;vertical-align:middle}
.dot.amber{background:#f59e0b}
.dot.olive{background:#84cc16}
.dot.bronze{background:#d97706}
.dot.terra{background:#92400e}
.tree .sub{list-style:none;padding-left:12px;overflow:hidden;display:none}
.tree li.open>.sub{display:block}
.main-content{flex:1;overflow-y:auto;padding:1rem}
.main-inner{max-width:1000px;margin:0 auto}
.readme-section{border:1px solid var(--stroke);background:rgba(0,0,0,.5);padding:1rem 1.25rem}
.readme-section h2{color:var(--amber);font-size:13px;text-transform:uppercase;letter-spacing:.06em;margin-bottom:0.75rem;padding-bottom:0.5rem;border-bottom:1px solid var(--stroke)}
.readme-section .readme-content{color:var(--text);font-size:12px;line-height:1.6}
.readme-section .readme-content h1,.readme-section .readme-content h2,.readme-section .readme-content h3{color:var(--amber);margin-top:1.25rem;margin-bottom:0.4rem}
.readme-section .readme-content pre{background:rgba(245,158,11,0.05);border:1px solid var(--stroke);padding:0.75rem;overflow-x:auto;font-size:11px}
.readme-section .readme-content code{font-family:inherit;font-size:0.95em}
.readme-section .readme-content a{color:var(--amber)}
.readme-section .readme-content p{margin-bottom:0.6rem}
.readme-section .readme-content ul,.readme-section .readme-content ol{margin-left:1.25rem;margin-bottom:0.6rem}
.file-view{border:1px solid var(--stroke);background:rgba(0,0,0,.5);overflow-x:auto}
.file-view pre{margin:0;padding:1rem;font-size:12px;line-height:1.5;tab-size:4;color:var(--text)}
.file-view .ln{color:rgba(245,158,11,.4);user-select:none;display:inline-block;min-width:3em;text-align:right;margin-right:1em}
.file-view-meta{margin-bottom:0.5rem;font-size:11px;color:var(--muted)}
.dock{position:relative;height:110px;background:rgba(5,5,5,.94);border-top:1px solid var(--amber);box-shadow:0 -8px 30px rgba(0,0,0,.8);transition:height .28s ease;z-index:50;flex-shrink:0}
.dock.expanded{height:320px}
.dock-toggle{position:absolute;top:-14px;left:50%;transform:translateX(-50%);width:46px;height:16px;background:#000;border:1px solid var(--amber);border-bottom:none;border-radius:4px 4px 0 0;color:var(--amber);font-size:10px;line-height:14px;cursor:pointer;letter-spacing:.1em}
.dock-toggle:hover{background:#0a0a0a}
.dock-inner{height:100%;padding:8px;overflow:hidden}
.grid{display:grid;grid-template-columns:1fr 210px 220px 260px;gap:8px;height:100%}
.panel{border:1px solid var(--stroke);background:rgba(0,0,0,.55);display:flex;flex-direction:column;min-height:0;transition:border-color .15s}
.panel:hover{border-color:var(--amber)}
.panel-header{display:flex;align-items:center;justify-content:space-between;padding:2px 6px 3px;border-bottom:1px solid var(--stroke);background:var(--amber-bg);color:var(--amber);text-transform:uppercase;letter-spacing:.08em;font-weight:600;font-size:10px}
.panel-header .ico{cursor:pointer;opacity:.8;text-transform:lowercase;border:1px solid var(--stroke);padding:0 4px;border-radius:2px;background:rgba(0,0,0,0.4)}
.panel-header .ico:hover{opacity:1;border-color:var(--amber)}
.panel-body{flex:1;min-height:0;padding:5px 6px;display:flex;flex-direction:column;gap:5px;overflow:hidden}
.panel.collapsed .panel-body{display:none}
.cam{display:grid;grid-template-columns:1fr auto;gap:6px;align-items:center;padding:2px 0;border-bottom:1px dotted rgba(245,158,11,.14)}
.cam:last-child{border-bottom:none}
.cam .name{color:#e8e8e8}
.cam .meta{color:var(--muted)}
.cam .meta b{color:var(--amber);font-weight:600}
.kv{display:flex;align-items:center;gap:6px;padding:1px 0;border-bottom:1px dotted rgba(245,158,11,.14);white-space:nowrap}
.kv:last-child{border-bottom:none}
.kv .name{color:var(--muted);flex-shrink:0}
.kv .val{color:#e8e8e8}
.kv .val b{color:var(--amber);font-weight:600}
.info-split{display:flex;flex-direction:column;gap:0;height:100%;overflow-y:auto}
.info-split .subhead{padding:4px 6px 2px;margin:0}
.site-footer{flex-shrink:0;height:1.5rem;background:#000;color:var(--muted);border-top:1px solid rgba(245,158,11,.25);display:flex;align-items:center;justify-content:space-between;padding:0 10px;font-size:10px;z-index:60}
.site-footer a{color:var(--muted);text-decoration:none;margin-left:0.75rem}
.site-footer a:hover{color:var(--amber)}
.console-bar{display:flex;align-items:center;gap:0;border-bottom:1px solid var(--stroke);background:var(--amber-bg);flex-shrink:0;height:26px}
.tab{padding:4px 10px;color:var(--muted);cursor:pointer;border:1px solid transparent;border-bottom:none;font-size:10px;text-transform:uppercase;letter-spacing:.06em;background:none;height:100%;display:flex;align-items:center}
.tab:hover{color:var(--amber)}
.tab.active{color:var(--amber);border-color:var(--stroke);background:rgba(0,0,0,.4);font-weight:600}
.tab.locked{color:rgba(138,138,138,.4);cursor:default;pointer-events:none}
.tab.locked.active{color:rgba(138,138,138,.4);border-color:transparent;background:none;font-weight:400}
.console-auth{margin-left:auto;display:flex;align-items:center;gap:6px;padding-right:8px}
.console-auth input{background:#000;color:var(--text);border:1px solid var(--stroke);padding:2px 6px;font-family:inherit;font-size:10px;width:10rem;border-radius:2px}
.console-auth button{background:rgba(245,158,11,.15);color:var(--amber);border:1px solid var(--stroke);padding:2px 8px;border-radius:2px;cursor:pointer;font-family:inherit;font-size:10px}
.console-auth button:hover{background:rgba(245,158,11,.25)}
.console-auth span{font-size:10px;color:var(--muted)}
.tab-panes{flex:1;min-height:0;position:relative}
.tab-pane{position:absolute;inset:0;display:none}
.tab-pane.active{display:block}
.tab-pane iframe{width:100%;height:100%;border:none;background:#000}
.deliv{display:flex;flex-direction:column;gap:4px}
.drow{display:grid;grid-template-columns:1fr auto;gap:6px;align-items:center;padding:2px 0;border-bottom:1px dotted rgba(245,158,11,.12)}
.drow:last-child{border-bottom:none}
.dname{color:#e2e2e2}
.dval{color:var(--muted);text-align:right}
.dval b{color:var(--amber)}
.extra{margin-top:4px;padding-top:4px;border-top:1px solid rgba(245,158,11,.18);display:flex;flex-direction:column;gap:4px;opacity:.98}
.subhead{color:var(--amber);text-transform:uppercase;letter-spacing:.06em;font-size:10px;opacity:.9}
.chat{display:flex;flex-direction:column;gap:3px;max-height:78px;overflow:auto;padding-right:2px}
.msg{border-left:2px solid rgba(245,158,11,.35);padding-left:4px}
.msg .who{color:var(--amber)}
.msg .time{color:var(--muted);margin-left:4px}
.spark{width:48px;height:14px;opacity:.9;flex-shrink:0}
.shotlist{display:grid;grid-template-columns:1fr auto;row-gap:2px;column-gap:6px}
.shotlist .chk{color:var(--amber)}
.bar{height:4px;background:rgba(245,158,11,.18);border:1px solid rgba(245,158,11,.3);position:relative;overflow:hidden}
.bar>i{position:absolute;left:0;top:0;bottom:0;background:var(--amber);width:0}
@media (max-width:900px){
  .grid{grid-template-columns:1fr;grid-auto-rows:min-content;overflow:auto}
  .dock{height:170px}
  .dock.expanded{height:88vh}
}
</style>
</head>
<body>
<div class="main-area">
  <aside class="sidebar">
    <div class="panel">
      <div class="panel-head"><a href="/" style="color:var(--amber);text-decoration:none">{{.RepoName}}</a></div>
      <div style="padding:6px 8px;display:flex;flex-direction:column;gap:2px;font-size:11px">
        <div><a href="/docs/" style="color:var(--amber);text-decoration:none">Documentation</a></div>
        <div><a href="/api/" style="color:var(--amber);text-decoration:none">API Reference</a></div>
        <div><a href="/server/" style="color:var(--amber);text-decoration:none">Server Info</a></div>
      </div>
    </div>
    <div class="panel tree-panel">
      <div class="panel-head">Filesystem</div>
      <ul class="tree" id="tree"></ul>
    </div>
    <div class="panel search-panel" style="padding:0">
      <input class="search" id="q" placeholder="search code...">
    </div>
  </aside>
  <div class="main-content">
    <div class="main-inner" id="mainContent">
      {{if .ReadmeHTML}}
      <div class="readme-section">
        <h2>README</h2>
        <div class="readme-content">{{.ReadmeHTML}}</div>
      </div>
      {{end}}
    </div>
  </div>
</div>

<div class="dock expanded" id="dock">
  <button class="dock-toggle" id="dockToggle">▼</button>
  <div class="dock-inner">
    <div class="grid">
      <div class="panel" id="pConsole">
        <div class="console-bar" id="consoleTabs">
          <div class="tab locked active" data-pane="command">command</div>
          <div class="tab locked" data-pane="requests">requests</div>
          <div class="tab locked" data-pane="stdout">stdout</div>
          <div class="tab locked" data-pane="stderr">stderr</div>
          <span class="console-auth" id="consoleAuth">
            <input type="text" id="cmd-token" placeholder="token" autocomplete="off" spellcheck="false">
            <button id="cmd-btn" onclick="doConsoleAuth()">connect</button>
            <span id="cmd-status"></span>
          </span>
        </div>
        <div class="tab-panes" id="consolePanes">
          <div class="tab-pane active" data-pane="command"><iframe data-src="/wormhole/command/pane"></iframe></div>
          <div class="tab-pane" data-pane="requests"><iframe data-src="/wormhole/requests/pane"></iframe></div>
          <div class="tab-pane" data-pane="stdout"><iframe data-src="/wormhole/stdout/pane"></iframe></div>
          <div class="tab-pane" data-pane="stderr"><iframe data-src="/wormhole/stderr/pane"></iframe></div>
        </div>
      </div>

      <div class="panel" id="pCams">
        <div class="panel-header"><span>Host / Network</span><span class="ico" onclick="this.closest('.panel').classList.toggle('collapsed')">±</span></div>
        <div class="panel-body">
          <div class="cam">
            <div>
              <div class="name">eth0 (WAN)</div>
              <div class="meta"><b>14.2</b> GB in · <b>94%</b> link · 1Gbps</div>
            </div>
            <svg class="spark" viewBox="0 0 48 14"><polyline points="0,3 8,4 16,5 24,6 32,7 40,8 48,9" fill="none" stroke="#f59e0b" stroke-opacity=".9" stroke-width="1"/></svg>
          </div>
          <div class="cam">
            <div>
              <div class="name">eth1 (LAN)</div>
              <div class="meta"><b>8.1</b> GB out · <b>87%</b> link · 10Gbps</div>
            </div>
            <svg class="spark" viewBox="0 0 48 14"><polyline points="0,4 8,5 16,5 24,6 32,7 40,7 48,8" fill="none" stroke="#f59e0b" stroke-opacity=".8" stroke-width="1"/></svg>
          </div>
          <div class="cam">
            <div>
              <div class="name">CPU</div>
              <div class="meta"><b>12%</b> load · 64 Cores · 42°C</div>
            </div>
            <svg class="spark" viewBox="0 0 48 14"><polyline points="0,2 8,3 16,5 24,7 32,8 40,9 48,10" fill="none" stroke="#f59e0b" stroke-opacity=".75" stroke-width="1"/></svg>
          </div>
          <div class="cam">
            <div>
              <div class="name">RAM</div>
              <div class="meta"><b>41%</b> used · 128GB total</div>
            </div>
            <svg class="spark" viewBox="0 0 48 14"><polyline points="0,5 8,5 16,6 24,6 32,7 40,7 48,8" fill="none" stroke="#f59e0b" stroke-opacity=".7" stroke-width="1"/></svg>
          </div>
          <div class="extra">
            <div class="subhead">Firewall</div>
            <div class="shotlist">
              <div>Port 80 (HTTP)</div><div class="chk">ALLOW</div>
              <div>Port 443 (HTTPS)</div><div class="chk">ALLOW</div>
              <div>Port 22 (SSH)</div><div class="chk">RESTRICTED</div>
            </div>
          </div>
        </div>
      </div>

      <div class="panel" id="pDeliv">
        <div class="panel-header"><span>Server &amp; Application</span><span class="ico" onclick="this.closest('.panel').classList.toggle('collapsed')">±</span></div>
        <div class="panel-body">
          <div class="deliv">
            <div class="drow"><div class="dname">Server Setup</div><div class="dval"><b>84%</b></div></div>
            <div class="bar" style="margin:-2px 0 4px"><i style="width:84%"></i></div>
            <div class="drow"><div class="dname">Network</div><div class="dval">accretional_henosis</div></div>
            <div class="drow"><div class="dname">SSL Certs</div><div class="dval"><b>valid</b></div></div>
            <div class="drow"><div class="dname">Session</div><div class="dval" id="inf-sess">awaiting auth</div></div>
          </div>
          <div class="extra">
            <div class="subhead">Alerts</div>
            <div class="chat">
              <div class="msg"><span class="who">NOTICE</span><span class="time">—</span> boot awaiting authentication</div>
              <div class="msg"><span class="who">INFO</span><span class="time">—</span> server up</div>
            </div>
          </div>
        </div>
      </div>

      <div class="panel" id="pInfo">
        <div class="panel-header"><span>Service</span><span class="ico" onclick="this.closest('.panel').classList.toggle('collapsed')">±</span></div>
        <div class="panel-body" style="padding:5px 6px;gap:2px;overflow-y:auto">
          <div class="kv"><span class="name">Host</span> <span class="val" id="inf-host">—</span></div>
          <div class="kv"><span class="name">OS</span> <span class="val" id="inf-os">—</span></div>
          <div class="kv"><span class="name">Go</span> <span class="val" id="inf-go">—</span></div>
          <div class="kv"><span class="name">Uptime</span> <span class="val" id="inf-up">—</span></div>
          <div class="kv"><span class="name">Goroutines</span> <span class="val" id="inf-gr">—</span></div>
          <div class="kv"><span class="name">Heap</span> <span class="val" id="inf-heap">—</span></div>
          <div class="kv"><span class="name">Wormholes</span> <span class="val" id="inf-wh">—</span></div>
        </div>
      </div>
    </div>
  </div>
</div>

<footer class="site-footer">
  <span><b style="color:var(--amber)">{{.RepoName}}</b> — Status: <b style="color:var(--amber)">Alpha</b> — Built 2026</span>
  <span>Free &amp; Open Source by <a href="https://accretional.com/">Accretional</a> © <a href="#">Privacy</a> <a href="#">Terms</a></span>
</footer>

<script>
// Dock toggle
var dock=document.getElementById('dock'),toggle=document.getElementById('dockToggle');
toggle.addEventListener('click',function(){dock.classList.toggle('expanded');toggle.textContent=dock.classList.contains('expanded')?'▼':'▲'});

// Console tabs — only respond to unlocked tabs
document.getElementById('consoleTabs').addEventListener('click',function(e){
  var tab=e.target.closest('.tab'); if(!tab||tab.classList.contains('locked')) return;
  var pane=tab.dataset.pane;
  document.querySelectorAll('#consoleTabs .tab').forEach(function(t){t.classList.toggle('active',t.dataset.pane===pane)});
  document.querySelectorAll('#consolePanes .tab-pane').forEach(function(p){p.classList.toggle('active',p.dataset.pane===pane)});
});

// Unlock console tabs and load wormhole iframes
function unlockConsole(){
  document.querySelectorAll('#consoleTabs .tab.locked').forEach(function(t){t.classList.remove('locked')});
  document.querySelectorAll('#consolePanes .tab-pane iframe').forEach(function(f){
    if(f.dataset.src&&!f.src){f.src=f.dataset.src}
  });
}

// Auth result from command pane
window.addEventListener('message',function(e){
  if(e.data&&e.data.type==='command-auth'&&e.data.ok){
    document.getElementById('cmd-token').style.display='none';
    document.getElementById('cmd-btn').style.display='none';
    document.getElementById('cmd-status').textContent='authenticated';
    document.getElementById('cmd-status').style.color='#28a745';
    document.getElementById('inf-sess').innerHTML='<b>authenticated</b>';
  }
});

// Forward auth from console bar to command pane iframe
function doConsoleAuth(){
  var token=document.getElementById('cmd-token').value.trim();
  if(!token)return;
  // Unlock tabs and load all iframes (including command pane)
  unlockConsole();
  // Wait briefly for command iframe to load, then forward token
  var iframe=document.querySelector('.tab-pane[data-pane="command"] iframe');
  function tryAuth(){
    try{iframe.contentWindow.postMessage({type:'do-auth',token:token},'*')}catch(e){}
  }
  if(iframe.contentWindow&&iframe.src){
    // iframe may need a moment to initialize
    setTimeout(tryAuth,500);
  }
  // Switch to command tab
  document.querySelector('#consoleTabs .tab[data-pane="command"]').click();
}

// Load filesystem tree
(function(){
  var tree=document.getElementById('tree');
  var colors=['amber','olive','bronze','terra'];
  var ci=0;
  function renderTree(items,ul,prefix){
    items.forEach(function(item,idx){
      var li=document.createElement('li');
      var dot=document.createElement('span');
      dot.className='dot '+colors[ci%colors.length]; ci++;
      li.appendChild(dot);
      var path=prefix?prefix+'/'+item.name:item.name;
      if(item.dir){
        li.className='dir';
        var a=document.createElement('a');
        a.href='#';
        a.textContent=item.name+'/';
        a.onclick=function(e){e.preventDefault();li.classList.toggle('open');};
        li.appendChild(a);
        if(item.count){
          var cnt=document.createElement('span');
          cnt.className='cnt';
          cnt.textContent=item.count+' items';
          li.appendChild(cnt);
        }
        if(item.items&&item.items.length){
          var sub=document.createElement('ul');
          sub.className='sub';
          renderTree(item.items,sub,path);
          li.appendChild(sub);
        }
      } else {
        var a=document.createElement('a');
        a.href='#';
        a.textContent=item.name;
        a.onclick=function(e){e.preventDefault();loadFile(path,item.name);};
        li.appendChild(a);
        if(item.size){
          var sz=document.createElement('span');
          sz.className='sz';
          sz.textContent=item.size;
          li.appendChild(sz);
        }
      }
      ul.appendChild(li);
    });
  }
  fetch('/source/tree.json').then(function(r){return r.json()}).then(function(data){
    renderTree(data,tree,'');
  });
})();

// Load file into main content area
function loadFile(path,name){
  var main=document.getElementById('mainContent');
  main.innerHTML='<div class="file-view-meta">Loading '+path+'...</div>';
  fetch('/source/raw/'+path).then(function(r){
    if(!r.ok) throw new Error('not found');
    return r.text();
  }).then(function(text){
    var lines=text.split('\n');
    var html='<div class="file-view-meta"><b style="color:var(--amber)">'+name+'</b> <span>'+lines.length+' lines</span></div>';
    html+='<div class="file-view"><pre>';
    lines.forEach(function(line,i){
      html+='<span class="ln">'+(i+1)+'</span>  '+escapeHtml(line)+'\n';
    });
    html+='</pre></div>';
    main.innerHTML=html;
  }).catch(function(){
    main.innerHTML='<div class="file-view-meta" style="color:#d73a49">Could not load '+path+'</div>';
  });
}
function escapeHtml(s){
  return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');
}

// Search filtering
document.getElementById('q').addEventListener('input',function(){
  var q=this.value.toLowerCase();
  document.querySelectorAll('#tree li').forEach(function(li){
    var text=li.textContent.toLowerCase();
    li.style.display=(!q||text.indexOf(q)>=0)?'':'none';
    if(q&&text.indexOf(q)>=0){
      var p=li.parentElement;
      while(p&&p.id!=='tree'){
        if(p.tagName==='LI')p.classList.add('open');
        p=p.parentElement;
      }
    }
  });
});

// Fetch server info for right panel
(function(){
  var x=new XMLHttpRequest();
  x.open('GET','/server/');
  x.responseType='document';
  x.onload=function(){
    if(x.status!==200)return;
    var doc=x.responseXML; if(!doc)return;
    var rows=doc.querySelectorAll('.file-list td');
    var map={};
    for(var i=0;i<rows.length;i+=2){
      if(rows[i]&&rows[i+1]) map[rows[i].textContent.trim()]=rows[i+1].textContent.trim();
    }
    if(map['Hostname'])document.getElementById('inf-host').innerHTML='<b>'+map['Hostname']+'</b>';
    if(map['Go Version'])document.getElementById('inf-go').innerHTML='<b>'+map['Go Version']+'</b>';
    if(map['OS / Arch'])document.getElementById('inf-os').innerHTML='<b>'+map['OS / Arch']+'</b>';
    if(map['Uptime'])document.getElementById('inf-up').innerHTML='<b>'+map['Uptime']+'</b>';
    if(map['Goroutines'])document.getElementById('inf-gr').innerHTML='<b>'+map['Goroutines']+'</b>';
    if(map['Heap'])document.getElementById('inf-heap').innerHTML='<b>'+map['Heap']+'</b>';
    document.getElementById('inf-wh').innerHTML='<b>4 streams</b>';
  };
  x.send();
})();
</script>

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
