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
	"github.com/accretional/anyserver/wormhole"
)

//go:embed all:source
var sourceFS embed.FS

//go:embed all:static
var staticFS embed.FS

//go:embed swagger.json
var swaggerJSON []byte

//go:embed build.binarypb
var buildLogPB []byte

//go:embed tests.binarypb
var testLogPB []byte

//go:embed api.html
var apiHTML []byte

//go:embed docs.html
var docsHTML []byte

func main() {
	// Capture stdout/stderr before any output occurs
	reg := wormhole.NewRegistry()
	if err := wormhole.CaptureOutputs(reg); err != nil {
		fmt.Fprintf(os.Stderr, "wormhole capture failed: %v\n", err)
	}

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
		APIHTML:     apiHTML,
		DocsHTML:    docsHTML,
		BuildLogPB:  buildLogPB,
		TestLogPB:   testLogPB,
		ReadmeHTML:  readmeHTML,
		Wormholes:   reg,
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
