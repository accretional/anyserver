// Package docs implements the Docs gRPC service, which provides streaming
// source code browsing backed by an embedded filesystem.
package docs

import (
	"io/fs"
	"mime"
	"path/filepath"
	"strings"

	pb "github.com/accretional/anyserver/proto/docs"
	openformatv1 "github.com/accretional/anyserver/proto/openformat/v1"
)

const chunkSize = 32 * 1024 // 32KB chunks for streaming

// Service implements the Docs gRPC service.
type Service struct {
	pb.UnimplementedDocsServer
	sourceFS fs.FS
}

// New creates a Docs service backed by the given filesystem.
func New(sourceFS fs.FS) *Service {
	return &Service{sourceFS: sourceFS}
}

// Source streams the contents of a path from the embedded source tree.
func (s *Service) Source(req *pb.SourceRequest, stream pb.Docs_SourceServer) error {
	path := normalizePath(req.Path)

	// Try as directory first
	entries, err := fs.ReadDir(s.sourceFS, path)
	if err == nil {
		// It's a directory — stream Path entries
		for _, e := range entries {
			info, _ := e.Info()
			var size int64
			if info != nil {
				size = info.Size()
			}
			if err := stream.Send(&pb.SourceCode{
				Kind: &pb.SourceCode_Path{
					Path: &pb.PathEntry{
						Name:  e.Name(),
						IsDir: e.IsDir(),
						Size:  size,
					},
				},
			}); err != nil {
				return err
			}
		}
		return nil
	}

	// Try as file
	data, err := fs.ReadFile(s.sourceFS, path)
	if err != nil {
		return err
	}

	filename := filepath.Base(path)
	ext := strings.ToLower(filepath.Ext(filename))

	// Classify by extension
	switch {
	case isTextExt(ext):
		return streamCode(stream, data, filename)
	case isMediaExt(ext):
		return streamMedia(stream, data, filename, ext)
	default:
		// Check if it looks like text
		if isLikelyText(data) {
			return streamCode(stream, data, filename)
		}
		return streamBinary(stream, data)
	}
}

func streamCode(stream pb.Docs_SourceServer, data []byte, filename string) error {
	for i := 0; i < len(data); i += chunkSize {
		end := i + chunkSize
		if end > len(data) {
			end = len(data)
		}
		if err := stream.Send(&pb.SourceCode{
			Kind: &pb.SourceCode_Code{
				Code: &pb.Code{
					Contents: string(data[i:end]),
					Filename: filename,
				},
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

func streamMedia(stream pb.Docs_SourceServer, data []byte, filename string, ext string) error {
	mt := detectMimeType(ext)
	for i := 0; i < len(data); i += chunkSize {
		end := i + chunkSize
		if end > len(data) {
			end = len(data)
		}
		if err := stream.Send(&pb.SourceCode{
			Kind: &pb.SourceCode_Media{
				Media: &pb.Media{
					Contents: data[i:end],
					Filename: filename,
					MimeType: mt,
				},
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

func streamBinary(stream pb.Docs_SourceServer, data []byte) error {
	for i := 0; i < len(data); i += chunkSize {
		end := i + chunkSize
		if end > len(data) {
			end = len(data)
		}
		if err := stream.Send(&pb.SourceCode{
			Kind: &pb.SourceCode_Binary{
				Binary: &pb.Binary{
					Contents: data[i:end],
				},
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

func normalizePath(p string) string {
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		p = "."
	}
	return filepath.Clean(p)
}

var textExts = map[string]bool{
	".go": true, ".proto": true, ".sh": true, ".bash": true,
	".md": true, ".txt": true, ".json": true, ".yaml": true, ".yml": true,
	".toml": true, ".xml": true, ".html": true, ".htm": true,
	".css": true, ".js": true, ".ts": true, ".tsx": true, ".jsx": true,
	".py": true, ".rs": true, ".c": true, ".h": true, ".cpp": true,
	".java": true, ".rb": true, ".sql": true, ".graphql": true,
	".dockerfile": true, ".gitignore": true, ".dockerignore": true,
	".env": true, ".cfg": true, ".ini": true, ".conf": true,
	".mod": true, ".sum": true, ".lock": true, ".csv": true,
	".textproto": true, ".pbtxt": true, ".makefile": true,
}

func isTextExt(ext string) bool {
	return textExts[ext]
}

var mediaExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".svg": true,
	".webp": true, ".ico": true, ".bmp": true,
	".mp3": true, ".wav": true, ".ogg": true, ".flac": true, ".aac": true,
	".mp4": true, ".webm": true, ".avi": true, ".mov": true,
}

func isMediaExt(ext string) bool {
	return mediaExts[ext]
}

func isLikelyText(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	check := data
	if len(check) > 512 {
		check = check[:512]
	}
	for _, b := range check {
		if b == 0 {
			return false
		}
	}
	return true
}

func detectMimeType(ext string) *openformatv1.MimeType {
	mimeStr := mime.TypeByExtension(ext)
	if mimeStr == "" {
		return &openformatv1.MimeType{
			Type:    &openformatv1.MimeType_DiscreteMediaType{DiscreteMediaType: openformatv1.DiscreteType_application},
			SubType: "octet-stream",
		}
	}

	parts := strings.SplitN(mimeStr, "/", 2)
	if len(parts) != 2 {
		return &openformatv1.MimeType{
			Type:    &openformatv1.MimeType_DiscreteMediaType{DiscreteMediaType: openformatv1.DiscreteType_application},
			SubType: "octet-stream",
		}
	}

	topLevel := parts[0]
	subType := strings.SplitN(parts[1], ";", 2)[0] // strip params

	var dt openformatv1.DiscreteType
	switch topLevel {
	case "text":
		dt = openformatv1.DiscreteType_text
	case "image":
		dt = openformatv1.DiscreteType_image
	case "audio":
		dt = openformatv1.DiscreteType_audio
	case "video":
		dt = openformatv1.DiscreteType_video
	case "font":
		dt = openformatv1.DiscreteType_font
	case "model":
		dt = openformatv1.DiscreteType_model
	default:
		dt = openformatv1.DiscreteType_application
	}

	return &openformatv1.MimeType{
		Type:    &openformatv1.MimeType_DiscreteMediaType{DiscreteMediaType: dt},
		SubType: subType,
	}
}
