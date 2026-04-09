package metrics

import (
	"fmt"
	"html/template"
	"net/http"
	"sort"

	pb "github.com/accretional/anyserver/proto/metrics"
)

// HTTPHandler returns an http.Handler for the /server/ page.
func (s *Service) HTTPHandler(repoName string) http.Handler {
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
<style>
html, body { height:100%; }
body { display:flex; flex-direction:column; }
.header { flex-shrink:0; }
.server-body { flex:1; overflow-y:auto; padding-bottom:1rem; }
.stream-pane { width:100%; height:300px; border:1px solid #d0d8f0; border-radius:4px; background:#1a1a2e; }
.command-footer { flex-shrink:0; border-top:2px solid #1a1a2e; background:#0d0d1a; }
.command-footer iframe { width:100%; height:220px; border:none; }
.site-footer { flex-shrink:0; height:1.5rem; background:#000; color:#888; border-top:1px solid #333; display:flex; align-items:center; justify-content:space-between; padding:0 1rem; font-size:0.7rem; }
.site-footer a { color:#666; text-decoration:none; margin-left:0.75rem; }
.site-footer a:hover { color:#aaa; }
.footer-links { display:flex; gap:0; }
</style>
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

<div class="server-body">
<main class="content">

<div class="server-streams">
  <div class="stream-col">
    <h2>Requests</h2>
    <iframe src="/wormhole/requests/pane" class="stream-pane"></iframe>
  </div>
  <div class="stream-col">
    <h2>stdout</h2>
    <iframe src="/wormhole/stdout/pane" class="stream-pane"></iframe>
  </div>
  <div class="stream-col">
    <h2>stderr</h2>
    <iframe src="/wormhole/stderr/pane" class="stream-pane"></iframe>
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
</div>

<div id="command-footer" class="command-footer" style="display:none;">
  <iframe src="/wormhole/command/pane"></iframe>
</div>
<footer class="site-footer">
  <span>{{.RepoName}}</span>
  <span class="footer-links">
    <a href="/docs/">Docs</a>
    <a href="/api/">API</a>
    <a href="#">Privacy</a>
    <a href="#">Terms</a>
  </span>
</footer>
<script>
window.addEventListener('message', function(e) {
  if (e.data && e.data.type === 'command-pane') {
    document.getElementById('command-footer').style.display = e.data.visible ? '' : 'none';
  }
});
</script>

</body>
</html>`
