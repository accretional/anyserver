package main

import (
	"fmt"
	"io"
	"os"

	pb "github.com/accretional/anyserver/proto/metrics"
	"google.golang.org/protobuf/proto"
)

// logpb reads stdin and writes a BuildLog or TestLog binarypb to the given file.
// Usage: logpb build <output.binarypb>
//        logpb test <output.binarypb>
func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "usage: logpb <build|test> <output.binarypb>\n")
		os.Exit(1)
	}

	kind := os.Args[1]
	outPath := os.Args[2]

	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read stdin: %v\n", err)
		os.Exit(1)
	}

	stdout := string(data)

	var msg proto.Message
	switch kind {
	case "build":
		msg = &pb.BuildLog{Stdout: stdout}
	case "test":
		msg = &pb.TestLog{Stdout: stdout}
	default:
		fmt.Fprintf(os.Stderr, "unknown kind %q (use build or test)\n", kind)
		os.Exit(1)
	}

	out, err := proto.Marshal(msg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(outPath, out, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "write: %v\n", err)
		os.Exit(1)
	}
}
