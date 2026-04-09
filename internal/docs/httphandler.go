package docs

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
)

// HTTPHandler returns an http.Handler that serves source browsing and docs as HTML.
func HTTPHandler(sourceFS fs.FS, docsFS fs.FS, repoName string, readmeHTML template.HTML) http.Handler {
	mux := http.NewServeMux()

	// Source tree JSON — shallow recursive listing for sidebar
	mux.HandleFunc("/source/tree.json", func(w http.ResponseWriter, r *http.Request) {
		type entry struct {
			Name  string   `json:"name"`
			Dir   bool     `json:"dir,omitempty"`
			Size  string   `json:"size,omitempty"`
			Count int      `json:"count,omitempty"`
			Items []*entry `json:"items,omitempty"`
		}
		var walk func(dir string, depth int) []*entry
		walk = func(dir string, depth int) []*entry {
			entries, err := fs.ReadDir(sourceFS, dir)
			if err != nil {
				return nil
			}
			var out []*entry
			for _, e := range entries {
				p := dir + "/" + e.Name()
				if dir == "." {
					p = e.Name()
				}
				node := &entry{Name: e.Name(), Dir: e.IsDir()}
				if !e.IsDir() {
					if info, err := e.Info(); err == nil {
						node.Size = formatSize(info.Size())
					}
				}
				if e.IsDir() {
					if subs, err := fs.ReadDir(sourceFS, p); err == nil {
						node.Count = len(subs)
					}
					if depth < 3 {
						node.Items = walk(p, depth+1)
					}
				}
				out = append(out, node)
			}
			return out
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(walk(".", 0))
	})

	// Raw file content for inline viewer
	mux.HandleFunc("/source/raw/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/source/raw/")
		path = normalizePath(path)
		data, err := fs.ReadFile(sourceFS, path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(data)
	})

	// Source browsing — redirect to root page with sidebar
	mux.HandleFunc("/source/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/source/")
		if path == "" || path == "/" {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		// Serve raw files for direct links
		path = normalizePath(path)
		data, err := fs.ReadFile(sourceFS, path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(data)
	})

	// Docs (godoc HTML)
	if docsFS != nil {
		mux.Handle("/docs/", http.StripPrefix("/docs/", http.FileServer(http.FS(docsFS))))
	}

	return mux
}


func formatSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}
