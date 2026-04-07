package anyserver

// Services is the declarative list of gRPC services to include in this server.
// Each entry is a Go module path that exports a Register(*grpc.Server) function.
// tools/gen.sh reads this list to auto-generate the wiring code.
//
// To add a service: append its module path here, then run tools/gen.sh.
var Services = []string{
	// Built-in
	"github.com/accretional/anyserver/internal/docs",

	// External (injected via tools/gen.sh --inject)
	// "github.com/accretional/vad",
	// "github.com/accretional/ffmpeg-proto",
}
