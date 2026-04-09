package wormhole

import (
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
)

// HTTPHandler returns an http.Handler that serves the wormhole discovery page
// and streaming endpoints under /wormhole/.
func HTTPHandler(reg *Registry, repoName string) http.Handler {
	discoveryTmpl := template.Must(template.New("wormhole").Parse(discoveryTemplate))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/wormhole/")
		path = strings.TrimSuffix(path, "/")

		// Multi-stream via query param
		if kinds := r.URL.Query().Get("kinds"); kinds != "" && path == "" {
			serveMultiStream(w, r, reg, kinds)
			return
		}

		// Discovery page
		if path == "" {
			serveDiscovery(w, r, reg, repoName, discoveryTmpl)
			return
		}

		// Single wormhole stream or tail
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

		// Live stream
		serveStream(w, r, wh, "")
	})
}

func serveDiscovery(w http.ResponseWriter, r *http.Request, reg *Registry, repoName string, tmpl *template.Template) {
	type whInfo struct {
		Kind        Kind
		Description string
		Subscribers int64
	}

	whs := reg.All()
	infos := make([]whInfo, len(whs))
	for i, wh := range whs {
		infos[i] = whInfo{
			Kind:        wh.Kind(),
			Description: wh.Description(),
			Subscribers: wh.Subscribers(),
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl.Execute(w, struct {
		RepoName  string
		Wormholes []whInfo
	}{repoName, infos})
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

func serveStream(w http.ResponseWriter, r *http.Request, wh *Wormhole, prefix string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	// Replay ring buffer
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
			if prefix != "" {
				fmt.Fprintf(w, "[%s] %s", prefix, line)
			} else {
				w.Write(line)
			}
			flusher.Flush()
		}
	}
}

func serveMultiStream(w http.ResponseWriter, r *http.Request, reg *Registry, kindsParam string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	kindNames := strings.Split(kindsParam, ",")
	type sub struct {
		kind Kind
		ch   <-chan []byte
		unsub func()
	}

	var subs []sub
	for _, name := range kindNames {
		name = strings.TrimSpace(name)
		wh := reg.Get(Kind(name))
		if wh == nil {
			continue
		}
		ch, unsub := wh.Subscribe()
		subs = append(subs, sub{Kind(name), ch, unsub})
	}

	if len(subs) == 0 {
		http.NotFound(w, r)
		return
	}

	defer func() {
		for _, s := range subs {
			s.unsub()
		}
	}()

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	// Replay ring buffers
	for _, s := range subs {
		wh := reg.Get(s.kind)
		if wh != nil {
			lines := wh.Tail(50)
			for _, line := range lines {
				fmt.Fprintf(w, "[%s] %s", s.kind, line)
			}
		}
	}
	flusher.Flush()

	// Merge channels using goroutines feeding a single output channel
	merged := make(chan struct {
		kind Kind
		data []byte
	}, 256)

	ctx := r.Context()
	for _, s := range subs {
		go func(kind Kind, ch <-chan []byte) {
			for {
				select {
				case <-ctx.Done():
					return
				case line, ok := <-ch:
					if !ok {
						return
					}
					select {
					case merged <- struct {
						kind Kind
						data []byte
					}{kind, line}:
					case <-ctx.Done():
						return
					}
				}
			}
		}(s.kind, s.ch)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-merged:
			fmt.Fprintf(w, "[%s] %s", msg.kind, msg.data)
			flusher.Flush()
		}
	}
}

const discoveryTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Wormhole - {{.RepoName}}</title>
<link rel="stylesheet" href="/static/docs.css">
<script src="https://unpkg.com/htmx.org@2.0.4"></script>
</head>
<body>
<header class="header">
  <a href="/" class="header-title">{{.RepoName}}</a>
  <nav class="header-nav">
    <a href="/source/">Source</a>
    <a href="/docs/">Docs</a>
    <a href="/api/">API</a>
    <a href="/server/">Server</a>
    <a href="/wormhole/">Wormhole</a>
  </nav>
</header>
<main class="content">

<section class="index-section">
  <h2>Wormholes</h2>
  <p>Named server streams. Connect to any combination via HTTP.</p>
  <table class="file-list">
    <thead><tr><th>Kind</th><th>Description</th><th>Subscribers</th><th>Stream</th></tr></thead>
    <tbody>
    {{range .Wormholes}}
    <tr>
      <td><code>{{.Kind}}</code></td>
      <td>{{.Description}}</td>
      <td>{{.Subscribers}}</td>
      <td><a href="/wormhole/{{.Kind}}">raw</a></td>
    </tr>
    {{end}}
    </tbody>
  </table>
</section>

<section class="index-section">
  <h2>Usage</h2>
  <div class="code-block"><pre><code># Stream a single wormhole
curl -N http://localhost:PORT/wormhole/stdout

# Stream multiple wormholes (prefixed lines)
curl -N http://localhost:PORT/wormhole/?kinds=stdout,stderr

# Get last 50 lines (snapshot)
curl http://localhost:PORT/wormhole/stdout?tail=50</code></pre></div>
</section>

{{range .Wormholes}}
<section class="index-section">
  <h2>{{.Kind}}</h2>
  <div class="wormhole-tail"
       hx-get="/wormhole/{{.Kind}}?tail=30&fragment=true"
       hx-trigger="load, every 2s"
       hx-swap="innerHTML">
    <pre>(loading...)</pre>
  </div>
</section>
{{end}}

</main>
</body>
</html>`
