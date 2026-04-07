package docs

import (
	"fmt"
	"html"
	"html/template"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
)

// langForExt returns the language name for syntax highlighting hints.
func langForExt(ext string) string {
	switch ext {
	case ".go":
		return "go"
	case ".proto":
		return "protobuf"
	case ".sh", ".bash":
		return "bash"
	case ".js":
		return "javascript"
	case ".ts", ".tsx":
		return "typescript"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".css":
		return "css"
	case ".html", ".htm":
		return "html"
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".toml":
		return "toml"
	case ".sql":
		return "sql"
	case ".md":
		return "markdown"
	case ".xml":
		return "xml"
	case ".c", ".h":
		return "c"
	case ".cpp":
		return "cpp"
	case ".java":
		return "java"
	case ".rb":
		return "ruby"
	default:
		return "text"
	}
}

// HTTPHandler returns an http.Handler that serves source browsing and docs as HTML.
func HTTPHandler(sourceFS fs.FS, docsFS fs.FS, repoName string, readmeHTML template.HTML) http.Handler {
	mux := http.NewServeMux()

	// Source browsing
	mux.HandleFunc("/source/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/source/")
		path = normalizePath(path)

		// Try directory
		entries, err := fs.ReadDir(sourceFS, path)
		if err == nil {
			serveDirectory(w, path, entries, repoName)
			return
		}

		// Try file
		data, err := fs.ReadFile(sourceFS, path)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		filename := filepath.Base(path)
		ext := strings.ToLower(filepath.Ext(filename))

		if isTextExt(ext) || isLikelyText(data) {
			serveCodeFile(w, path, string(data), filename, repoName)
			return
		}

		if isMediaExt(ext) {
			serveMediaFile(w, data, filename, ext)
			return
		}

		// Binary: download
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(data)
	})

	// Docs (godoc HTML)
	if docsFS != nil {
		mux.Handle("/docs/", http.StripPrefix("/docs/", http.FileServer(http.FS(docsFS))))
	}

	return mux
}

func serveDirectory(w http.ResponseWriter, path string, entries []fs.DirEntry, repoName string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	displayPath := path
	if displayPath == "." {
		displayPath = "/"
	}

	// Build breadcrumbs
	breadcrumbs := buildBreadcrumbs(path)

	fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>%s - %s</title>
<link rel="stylesheet" href="/static/docs.css">
</head>
<body>
<header class="header">
  <a href="/" class="header-title">%s</a>
  <nav class="header-nav">
    <a href="/source/">Source</a>
    <a href="/docs/">Docs</a>
    <a href="/api/">API</a>
  </nav>
</header>
<main class="content">
<nav class="breadcrumbs">%s</nav>
<h2>%s</h2>
<table class="file-list">
<thead><tr><th>Name</th><th>Size</th></tr></thead>
<tbody>
`, html.EscapeString(displayPath), html.EscapeString(repoName),
		html.EscapeString(repoName), breadcrumbs, html.EscapeString(displayPath))

	// Parent link
	if path != "." {
		parent := filepath.Dir(path)
		if parent == "." {
			parent = ""
		}
		fmt.Fprintf(w, `<tr><td><a href="/source/%s">..</a></td><td></td></tr>`, parent)
	}

	for _, e := range entries {
		info, _ := e.Info()
		var size string
		icon := "📄"
		if e.IsDir() {
			icon = "📁"
			size = ""
		} else if info != nil {
			size = formatSize(info.Size())
		}
		entryPath := path + "/" + e.Name()
		if path == "." {
			entryPath = e.Name()
		}
		fmt.Fprintf(w, "<tr><td>%s <a href=\"/source/%s\">%s</a></td><td>%s</td></tr>\n",
			icon,
			html.EscapeString(entryPath),
			html.EscapeString(e.Name()),
			size)
	}

	fmt.Fprint(w, `</tbody></table></main></body></html>`)
}

func serveCodeFile(w http.ResponseWriter, path, contents, filename, repoName string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	ext := strings.ToLower(filepath.Ext(filename))
	lang := langForExt(ext)
	breadcrumbs := buildBreadcrumbs(path)

	// Count lines
	lines := strings.Count(contents, "\n") + 1

	fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>%s - %s</title>
<link rel="stylesheet" href="/static/docs.css">
</head>
<body>
<header class="header">
  <a href="/" class="header-title">%s</a>
  <nav class="header-nav">
    <a href="/source/">Source</a>
    <a href="/docs/">Docs</a>
    <a href="/api/">API</a>
  </nav>
</header>
<main class="content">
<nav class="breadcrumbs">%s</nav>
<div class="file-meta">
  <span class="file-lang">%s</span>
  <span class="file-lines">%d lines</span>
</div>
<div class="code-block">
<pre><code class="language-%s">`,
		html.EscapeString(filename), html.EscapeString(repoName),
		html.EscapeString(repoName), breadcrumbs,
		lang, lines, lang)

	// Write with line numbers
	for i, line := range strings.Split(contents, "\n") {
		fmt.Fprintf(w, `<span class="ln">%4d</span>  %s`+"\n", i+1, html.EscapeString(line))
	}

	fmt.Fprint(w, `</code></pre></div></main></body></html>`)
}

func serveMediaFile(w http.ResponseWriter, data []byte, filename, ext string) {
	mimeStr := "application/octet-stream"
	switch ext {
	case ".png":
		mimeStr = "image/png"
	case ".jpg", ".jpeg":
		mimeStr = "image/jpeg"
	case ".gif":
		mimeStr = "image/gif"
	case ".svg":
		mimeStr = "image/svg+xml"
	case ".webp":
		mimeStr = "image/webp"
	case ".mp3":
		mimeStr = "audio/mpeg"
	case ".wav":
		mimeStr = "audio/wav"
	case ".ogg":
		mimeStr = "audio/ogg"
	case ".mp4":
		mimeStr = "video/mp4"
	case ".webm":
		mimeStr = "video/webm"
	}
	w.Header().Set("Content-Type", mimeStr)
	w.Write(data)
}

func buildBreadcrumbs(path string) string {
	if path == "." || path == "" {
		return `<a href="/source/">root</a>`
	}
	var parts []string
	parts = append(parts, `<a href="/source/">root</a>`)
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		href := "/source/" + strings.Join(segments[:i+1], "/")
		if i == len(segments)-1 {
			parts = append(parts, html.EscapeString(seg))
		} else {
			parts = append(parts, fmt.Sprintf(`<a href="%s">%s</a>`, href, html.EscapeString(seg)))
		}
	}
	return strings.Join(parts, " / ")
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
