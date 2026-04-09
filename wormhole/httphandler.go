package wormhole

import (
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
)

// HTTPHandler returns an http.Handler that serves wormhole streaming
// endpoints under /wormhole/. The UI lives on /server/ instead.
func HTTPHandler(reg *Registry, repoName string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/wormhole/")
		path = strings.TrimSuffix(path, "/")

		// Multi-stream via query param
		if kinds := r.URL.Query().Get("kinds"); kinds != "" && path == "" {
			serveMultiStream(w, r, reg, kinds)
			return
		}

		// Redirect bare /wormhole/ to /server/
		if path == "" {
			http.Redirect(w, r, "/server/", http.StatusFound)
			return
		}

		// Command wormhole: delegate to WebSocket handler
		if path == "command" && reg.Command != nil {
			CommandHandler(reg.Command).ServeHTTP(w, r)
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
		kind  Kind
		ch    <-chan []byte
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

	// Merge channels
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
