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
		log.Printf("COMMAND TOKEN: %s", commandWH.Token())

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
<link rel="stylesheet" href="/static/base.css">
<link rel="stylesheet" href="/static/app.css">
</head>
<body class="app">
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
  <div class="document-panel">
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
<link rel="stylesheet" href="/static/base.css">
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
<link rel="stylesheet" href="/static/base.css">
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
<link rel="stylesheet" href="/static/base.css">
<link rel="stylesheet" href="/static/docs.css">
</head>
<body>
` + navHTML + `
<main class="content">
{{.Content}}
</main>
</body>
</html>`
