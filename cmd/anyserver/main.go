package main

import (
	"embed"
	"flag"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/accretional/anyserver"
)

//go:embed all:source
var sourceFS embed.FS

//go:embed all:static
var staticFS embed.FS

//go:embed swagger.json
var swaggerJSON []byte

func main() {
	port := flag.Int("port", 8080, "server port")
	repoName := flag.String("name", "anyserver", "repository/project name")
	flag.Parse()

	srcFS, err := fs.Sub(sourceFS, "source")
	if err != nil {
		log.Fatalf("Failed to access embedded source: %v", err)
	}

	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatalf("Failed to access embedded static: %v", err)
	}

	readmeHTML := loadReadmeHTML(srcFS)

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		fmt.Println("\nShutting down...")
		os.Exit(0)
	}()

	if err := anyserver.Run(anyserver.Config{
		Port:        *port,
		RepoName:    *repoName,
		SourceFS:    srcFS,
		StaticFS:    staticSub,
		SwaggerJSON: swaggerJSON,
		ReadmeHTML:  readmeHTML,
	}); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

func loadReadmeHTML(srcFS fs.FS) template.HTML {
	data, err := fs.ReadFile(srcFS, "README.md")
	if err != nil {
		return ""
	}
	return template.HTML("<pre>" + template.HTMLEscapeString(string(data)) + "</pre>")
}
