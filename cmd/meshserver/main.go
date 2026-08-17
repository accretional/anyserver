// Command meshserver runs anyserver's UI fronting the KVQ service mesh.
//
// Unlike cmd/anyserver (which go:embeds its own swagger/api.html at build
// time), meshserver reads an externally-generated swagger.json + api.html at
// startup and serves them through anyserver.Run. That swagger.json is produced
// by the meshadapter tool, which merges:
//   - live gRPC server reflection from mesh backends (e.g. chromerpc :50051),
//   - curated protobuf FileDescriptorSets from the KVQ TypeRegistry.
//
// The result: anyserver's /api/ "swagger-like" page presents EVERY integrated
// service/type in the mesh on one page.
//
// Usage:
//
//	meshserver -port 8092 -swagger /tmp/mesh.swagger.json -apihtml /tmp/mesh-api.html
package main

import (
	"embed"
	"flag"
	"io/fs"
	"log"
	"os"

	"github.com/accretional/anyserver"
)

//go:embed all:static
var staticFS embed.FS

func main() {
	port := flag.Int("port", 8092, "server port")
	name := flag.String("name", "KVQ Service Mesh", "page title")
	swaggerPath := flag.String("swagger", "/tmp/mesh.swagger.json", "merged swagger.json path")
	apiHTMLPath := flag.String("apihtml", "/tmp/mesh-api.html", "rendered api.html path")
	flag.Parse()

	swagger, err := os.ReadFile(*swaggerPath)
	if err != nil {
		log.Fatalf("read swagger: %v", err)
	}
	apiHTML, err := os.ReadFile(*apiHTMLPath)
	if err != nil {
		log.Fatalf("read api html: %v", err)
	}

	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatalf("static sub: %v", err)
	}
	// Reuse anyserver's own source tree as the SourceFS so the index renders.
	srcFS := os.DirFS(".")

	log.Printf("meshserver: serving mesh UI on :%d (swagger=%d bytes, apihtml=%d bytes)",
		*port, len(swagger), len(apiHTML))

	if err := anyserver.Run(anyserver.Config{
		Port:        *port,
		RepoName:    *name,
		SourceFS:    srcFS,
		StaticFS:    staticSub,
		SwaggerJSON: swagger,
		APIHTML:     apiHTML,
		ReadmeHTML:  "<pre>KVQ Service Mesh — unified API reference fronting chromerpc (live reflection) plus curated TypeRegistry descriptor sets.</pre>",
	}); err != nil {
		log.Fatalf("server: %v", err)
	}
}
