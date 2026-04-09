package wormhole

import (
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/websocket"
)

// HTTPHandler returns an http.Handler that serves wormhole streaming
// endpoints under /wormhole/.
//
// Endpoints:
//
//	GET /wormhole/{kind}       — stream (WebSocket if upgraded, else chunked HTTP)
//	GET /wormhole/{kind}/pane  — self-contained HTML pane (iframe + WebSocket)
//	GET /wormhole/{kind}?tail=N — snapshot of last N lines
//	GET /wormhole/command      — authenticated command WebSocket (see command_handler.go)
//	GET /wormhole/command/pane — command pane with auth UI and console
func HTTPHandler(reg *Registry, repoName string) http.Handler {
	paneTmpl := template.Must(template.New("pane").Parse(paneTemplate))
	cmdPaneTmpl := template.Must(template.New("cmdpane").Parse(commandPaneTemplate))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/wormhole/")
		path = strings.TrimSuffix(path, "/")

		// Redirect bare /wormhole/ to /server/
		if path == "" {
			http.Redirect(w, r, "/server/", http.StatusFound)
			return
		}

		// Command pane — always served; the pane probes the WebSocket
		// and uses postMessage to tell the parent whether to show it.
		if path == "command/pane" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			cmdPaneTmpl.Execute(w, nil)
			return
		}

		// Command WebSocket
		if path == "command" && reg.Command != nil {
			CommandHandler(reg.Command).ServeHTTP(w, r)
			return
		}

		// Check for /pane suffix
		if strings.HasSuffix(path, "/pane") {
			kind := Kind(strings.TrimSuffix(path, "/pane"))
			wh := reg.Get(kind)
			if wh == nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			paneTmpl.Execute(w, struct {
				Kind Kind
				Desc string
			}{kind, wh.Description()})
			return
		}

		// Single wormhole stream
		wh := reg.Get(Kind(path))
		if wh == nil {
			http.NotFound(w, r)
			return
		}

		// Tail snapshot
		if tailStr := r.URL.Query().Get("tail"); tailStr != "" {
			serveTail(w, r, wh, tailStr)
			return
		}

		// WebSocket upgrade — stream as text frames
		if isWebSocketUpgrade(r) {
			serveStreamWS(wh).ServeHTTP(w, r)
			return
		}

		// Chunked HTTP fallback (for curl)
		serveStreamHTTP(w, r, wh)
	})
}

func isWebSocketUpgrade(r *http.Request) bool {
	for _, v := range r.Header.Values("Connection") {
		if strings.EqualFold(strings.TrimSpace(v), "upgrade") {
			if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
				return true
			}
		}
	}
	return false
}

// serveStreamWS returns a WebSocket handler that streams wormhole data
// as text frames. Replays the ring buffer, then streams live.
func serveStreamWS(wh *Wormhole) http.Handler {
	return websocket.Server{
		Handshake: func(cfg *websocket.Config, r *http.Request) error {
			return nil // Accept any origin
		},
		Handler: func(ws *websocket.Conn) {
			defer ws.Close()

			// Replay ring buffer
			lines := wh.Tail(wh.ringSize)
			for _, line := range lines {
				if err := websocket.Message.Send(ws, string(line)); err != nil {
					return
				}
			}

			ch, unsub := wh.Subscribe()
			defer unsub()

			// Read from client in background (detect disconnect)
			done := make(chan struct{})
			go func() {
				defer close(done)
				var discard string
				for {
					if err := websocket.Message.Receive(ws, &discard); err != nil {
						return
					}
				}
			}()

			for {
				select {
				case <-done:
					return
				case line, ok := <-ch:
					if !ok {
						return
					}
					ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
					if err := websocket.Message.Send(ws, string(line)); err != nil {
						return
					}
				}
			}
		},
	}
}

// serveStreamHTTP streams wormhole data as chunked text/plain (for curl -N).
func serveStreamHTTP(w http.ResponseWriter, r *http.Request, wh *Wormhole) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	wh.WriteTo(w)
	flusher.Flush()

	ch, unsub := wh.Subscribe()
	defer unsub()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-ch:
			if !ok {
				return
			}
			w.Write(line)
			flusher.Flush()
		}
	}
}

func serveTail(w http.ResponseWriter, r *http.Request, wh *Wormhole, tailStr string) {
	n, err := strconv.Atoi(tailStr)
	if err != nil || n <= 0 {
		n = 50
	}

	lines := wh.Tail(n)

	fragment := r.URL.Query().Get("fragment") == "true"
	if fragment {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte("<pre>"))
		for _, line := range lines {
			template.HTMLEscape(w, line)
		}
		w.Write([]byte("</pre>"))
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	for _, line := range lines {
		w.Write(line)
	}
}

// ---------------------------------------------------------------------------
// Pane templates
//
// A "wormhole pane" is the fundamental UI building block:
//
//   iframe  +  WebSocket  =  pane
//
// The parent page is static HTML. Each pane is a self-contained HTML page
// loaded in an iframe. The pane opens a WebSocket to its wormhole endpoint
// and renders the stream into a <pre>. The pane is fully independent —
// it manages its own connection lifecycle, reconnection, and scrolling.
//
// This pattern is used everywhere:
//   - /server/ embeds panes for requests, stdout, stderr
//   - The command pane is a special pane anchored to the page footer
//   - Future pages will embed panes for their relevant streams
//
// The WebSocket carries raw text lines (one message per line). The pane
// appends each message to its <pre> element. No JSON parsing, no protocol
// overhead — just text in, text rendered.
//
// To add a new pane to any page:
//   <iframe src="/wormhole/{kind}/pane" class="stream-pane"></iframe>
//
// The pane auto-connects, replays buffered history, and streams live.
// ---------------------------------------------------------------------------

// paneTemplate is the self-contained HTML page for a read-only stream pane.
// It opens a WebSocket to /wormhole/{kind} and renders text into a <pre>.
const paneTemplate = `<!DOCTYPE html>
<html><head>
<meta charset="UTF-8">
<style>
* { margin:0; padding:0; box-sizing:border-box; }
html, body { height:100%; background:#000; color:#e0e0e0; font-family:"SF Mono",Menlo,Consolas,monospace; font-size:12px; }
#out { padding:8px; white-space:pre-wrap; word-break:break-all; line-height:1.4; }
.status { position:fixed; top:4px; right:8px; font-size:10px; opacity:0.5; }
</style>
</head><body>
<div class="status" id="st"></div>
<pre id="out"></pre>
<script>
var out = document.getElementById('out');
var st = document.getElementById('st');
function connect() {
  st.textContent = 'connecting...';
  var loc = window.location;
  var wsUrl = (loc.protocol==='https:'?'wss:':'ws:') + '//' + loc.host + '/wormhole/{{.Kind}}';
  var ws = new WebSocket(wsUrl);
  ws.onopen = function() { st.textContent = ''; };
  ws.onmessage = function(e) {
    out.textContent += e.data;
    window.scrollTo(0, document.body.scrollHeight);
  };
  ws.onclose = function() {
    st.textContent = 'reconnecting...';
    setTimeout(connect, 2000);
  };
}
connect();
</script>
</body></html>`

// commandPaneTemplate is the self-contained HTML page for the command pane.
// It handles auth inline (no separate UI needed) and shows a console with
// a cursor/position indicator.
const commandPaneTemplate = `<!DOCTYPE html>
<html><head>
<meta charset="UTF-8">
<style>
* { margin:0; padding:0; box-sizing:border-box; }
html, body { height:100%; background:#000; color:#e0e0e0; font-family:"SF Mono",Menlo,Consolas,monospace; font-size:12px; display:flex; flex-direction:column; }
#console { flex:1; overflow-y:auto; padding:8px; }
#out { white-space:pre-wrap; word-break:break-all; line-height:1.4; }
#cursor { display:inline-block; width:7px; height:14px; background:#e0e0e0; animation:blink 1s step-end infinite; vertical-align:text-bottom; }
@keyframes blink { 50% { opacity:0; } }
#pos { position:fixed; bottom:4px; right:8px; font-size:10px; opacity:0.4; }
.hidden { display:none; }
</style>
</head><body>
<div id="console" class="hidden">
  <pre id="out"></pre><span id="cursor"></span>
</div>
<div id="pos" class="hidden">0 lines</div>
<script>
var out = document.getElementById('out');
var cursor = document.getElementById('cursor');
var posEl = document.getElementById('pos');
var consoleEl = document.getElementById('console');
var lineCount = 0;
var ws = null;
var authenticated = false;
var pendingToken = null;

function notifyParent(type, data) {
  if (window.parent !== window) {
    data.type = type;
    window.parent.postMessage(data, '*');
  }
}

function updatePos() {
  posEl.textContent = lineCount + ' line' + (lineCount===1?'':'s');
}

function show() {
  consoleEl.classList.remove('hidden');
  posEl.classList.remove('hidden');
}

function doAuth(token) {
  if (!token) return;
  var loc = window.location;
  var wsUrl = (loc.protocol==='https:'?'wss:':'ws:') + '//' + loc.host + '/wormhole/command';
  ws = new WebSocket(wsUrl);
  ws.onopen = function() {
    ws.send(JSON.stringify({type:'auth', token:token}));
  };
  ws.onmessage = function(e) {
    var msg = JSON.parse(e.data);
    if (msg.type === 'auth_result') {
      if (msg.ok) {
        authenticated = true;
        notifyParent('command-auth', {ok:true});
      } else {
        notifyParent('command-auth', {ok:false});
      }
    } else if (msg.type === 'ping') {
      ws.send(JSON.stringify({type:'pong'}));
    } else if (msg.type === 'event') {
      out.textContent += msg.payload + '\n';
      lineCount++;
      updatePos();
      consoleEl.scrollTop = consoleEl.scrollHeight;
    }
  };
  ws.onclose = function() {
    if (authenticated) { cursor.style.display = 'none'; }
  };
  ws.onerror = function() {};
}
updatePos();
show();

window.addEventListener('message', function(e) {
  if (e.data && e.data.type === 'do-auth' && e.data.token) {
    doAuth(e.data.token);
  }
});
</script>
</body></html>`
