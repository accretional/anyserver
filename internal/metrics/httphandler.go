package metrics

import (
	"fmt"
	"html/template"
	"net/http"
	"sort"

	pb "github.com/accretional/anyserver/proto/metrics"
)

// HTTPHandler returns an http.Handler for the /server/ page.
func (s *Service) HTTPHandler(repoName string, hasCommand bool) http.Handler {
	tmpl := template.Must(template.New("server").Parse(serverTemplate))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/server/" {
			http.NotFound(w, r)
			return
		}

		staticResp, _ := s.Static(r.Context(), &pb.StaticRequest{})
		activeResp, _ := s.Active(r.Context(), &pb.ActiveRequest{})
		lifetimeResp, _ := s.Lifetime(r.Context(), &pb.LifetimeRequest{})

		// Format boot events
		var bootEvents []string
		if staticResp.BootLog != nil {
			for _, e := range staticResp.BootLog.Events {
				ts := ""
				if e.Timestamp != nil {
					ts = e.Timestamp.AsTime().Format("2006-01-02 15:04:05.000")
				}
				bootEvents = append(bootEvents, fmt.Sprintf("%s at %s", e.Status.String(), ts))
			}
		}

		// Sort paths by count descending
		type pathCount struct {
			Path  string
			Count int64
		}
		var paths []pathCount
		for p, c := range lifetimeResp.RequestsByPath {
			paths = append(paths, pathCount{p, c})
		}
		sort.Slice(paths, func(i, j int) bool { return paths[i].Count > paths[j].Count })

		// Sort status codes
		type statusCount struct {
			Code  int32
			Count int64
		}
		var statuses []statusCount
		for c, n := range lifetimeResp.RequestsByStatus {
			statuses = append(statuses, statusCount{c, n})
		}
		sort.Slice(statuses, func(i, j int) bool { return statuses[i].Code < statuses[j].Code })

		buildStdout := "(not embedded)"
		if staticResp.BuildLog != nil {
			buildStdout = staticResp.BuildLog.Stdout
		}
		testStdout := "(not embedded)"
		if staticResp.TestLog != nil {
			testStdout = staticResp.TestLog.Stdout
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		tmpl.Execute(w, struct {
			RepoName      string
			Hostname      string
			Port          int32
			GoVersion     string
			OS            string
			Arch          string
			Goroutines    int64
			HeapAllocMB   string
			SysMB         string
			NumGC         int64
			UptimeSeconds int64
			TotalRequests int64
			BootEvents    []string
			Paths         []pathCount
			Statuses      []statusCount
			BuildStdout   string
			TestStdout    string
			HasCommand    bool
		}{
			RepoName:      repoName,
			Hostname:      staticResp.Hostname,
			Port:          staticResp.Port,
			GoVersion:     staticResp.GoVersion,
			OS:            staticResp.Os,
			Arch:          staticResp.Arch,
			Goroutines:    activeResp.Goroutines,
			HeapAllocMB:   fmt.Sprintf("%.1f", float64(activeResp.HeapAllocBytes)/(1024*1024)),
			SysMB:         fmt.Sprintf("%.1f", float64(activeResp.SysBytes)/(1024*1024)),
			NumGC:         activeResp.NumGc,
			UptimeSeconds: lifetimeResp.UptimeSeconds,
			TotalRequests: lifetimeResp.TotalRequests,
			BootEvents:    bootEvents,
			Paths:         paths,
			Statuses:      statuses,
			BuildStdout:   buildStdout,
			TestStdout:    testStdout,
			HasCommand:    hasCommand,
		})
	})
}

const serverTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Server - {{.RepoName}}</title>
<link rel="stylesheet" href="/static/docs.css">
</head>
<body>
<header class="header">
  <a href="/" class="header-title">{{.RepoName}}</a>
  <nav class="header-nav">
    <a href="/source/">Source</a>
    <a href="/docs/">Docs</a>
    <a href="/api/">API</a>
    <a href="/server/">Server</a>
  </nav>
</header>
<main class="content">

{{if .HasCommand}}
<section class="index-section">
  <h2>Command</h2>
  <div class="command-auth">
    <input type="text" id="cmd-token" placeholder="Paste command token" autocomplete="off" spellcheck="false"
           style="font-family:monospace; padding:0.5rem; width:20rem; border:1px solid #d0d8f0; border-radius:4px;">
    <button id="cmd-btn" onclick="doAuth()"
            style="padding:0.5rem 1rem; background:#1a1a2e; color:#fff; border:none; border-radius:4px; cursor:pointer; margin-left:0.5rem;">
      Connect</button>
    <span id="cmd-status" style="margin-left:0.75rem; font-size:0.9rem;"></span>
  </div>
  <div id="cmd-log" class="wormhole-tail" style="margin-top:0.75rem; display:none;">
    <pre id="cmd-output"></pre>
  </div>
  <script>
  function doAuth() {
    var token = document.getElementById('cmd-token').value.trim();
    if (!token) return;
    var status = document.getElementById('cmd-status');
    var btn = document.getElementById('cmd-btn');
    var logDiv = document.getElementById('cmd-log');
    var output = document.getElementById('cmd-output');
    btn.disabled = true;
    status.textContent = 'connecting...';
    var loc = window.location;
    var wsUrl = (loc.protocol === 'https:' ? 'wss:' : 'ws:') + '//' + loc.host + '/wormhole/command';
    var ws = new WebSocket(wsUrl);
    ws.onopen = function() { ws.send(JSON.stringify({type:'auth', token:token})); };
    ws.onmessage = function(e) {
      var msg = JSON.parse(e.data);
      if (msg.type === 'auth_result') {
        if (msg.ok) {
          status.textContent = 'authenticated';
          status.style.color = '#28a745';
          logDiv.style.display = 'block';
          document.getElementById('cmd-token').disabled = true;
        } else {
          status.textContent = 'auth failed';
          status.style.color = '#d73a49';
          btn.disabled = false;
        }
      } else if (msg.type === 'ping') {
        ws.send(JSON.stringify({type:'pong'}));
      } else if (msg.type === 'event') {
        output.textContent += msg.payload + '\n';
        output.scrollTop = output.scrollHeight;
      }
    };
    ws.onclose = function() {
      if (status.textContent !== 'auth failed') {
        status.textContent = 'disconnected';
        status.style.color = '#d73a49';
      }
      btn.disabled = false;
    };
    ws.onerror = function() {
      status.textContent = 'connection error';
      status.style.color = '#d73a49';
      btn.disabled = false;
    };
    window._cmdWs = ws;
  }
  </script>
</section>
{{end}}

<div class="server-streams">
  <div class="stream-col">
    <h2>Requests</h2>
    <iframe src="/wormhole/requests" class="stream-frame"></iframe>
  </div>
  <div class="stream-col">
    <h2>stdout / stderr</h2>
    <iframe src="/wormhole/?kinds=stdout,stderr" class="stream-frame"></iframe>
  </div>
</div>

<section class="index-section">
  <h2>Server Info</h2>
  <table class="file-list">
    <tbody>
      <tr><td>Hostname</td><td>{{.Hostname}}</td></tr>
      <tr><td>Port</td><td>{{.Port}}</td></tr>
      <tr><td>Go Version</td><td>{{.GoVersion}}</td></tr>
      <tr><td>OS / Arch</td><td>{{.OS}} / {{.Arch}}</td></tr>
      <tr><td>Uptime</td><td>{{.UptimeSeconds}}s</td></tr>
      <tr><td>Goroutines</td><td>{{.Goroutines}}</td></tr>
      <tr><td>Heap</td><td>{{.HeapAllocMB}} MB</td></tr>
      <tr><td>Sys</td><td>{{.SysMB}} MB</td></tr>
      <tr><td>GC Cycles</td><td>{{.NumGC}}</td></tr>
      <tr><td>Total Requests</td><td>{{.TotalRequests}}</td></tr>
    </tbody>
  </table>
</section>

{{if .Statuses}}
<section class="index-section">
  <h2>Requests by Status</h2>
  <table class="file-list">
    <thead><tr><th>Status</th><th>Count</th></tr></thead>
    <tbody>
    {{range .Statuses}}<tr><td>{{.Code}}</td><td>{{.Count}}</td></tr>{{end}}
    </tbody>
  </table>
</section>
{{end}}

<section class="index-section">
  <h2>Boot Log</h2>
  {{if .BootEvents}}
  <ul>
    {{range .BootEvents}}<li>{{.}}</li>{{end}}
  </ul>
  {{else}}
  <p>No boot events recorded.</p>
  {{end}}
</section>

<section class="index-section">
  <h2>Build Log</h2>
  <div class="code-block"><pre><code>{{.BuildStdout}}</code></pre></div>
</section>

<section class="index-section">
  <h2>Test Log</h2>
  <div class="code-block"><pre><code>{{.TestStdout}}</code></pre></div>
</section>

</main>
</body>
</html>`
