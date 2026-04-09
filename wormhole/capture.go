package wormhole

import (
	"io"
	"os"
	"syscall"
)

// CaptureOutputs intercepts stdout and stderr at the file-descriptor level,
// registers them as wormholes in the given registry, and starts goroutines
// that fan out captured output to subscribers while still writing to the
// original terminal.
//
// This must be called at the very start of main(), before any logging or
// fmt.Print calls.
func CaptureOutputs(reg *Registry) error {
	stdoutWH := New(KindStdout, "Process standard output")
	stderrWH := New(KindStderr, "Process standard error (log output)")

	if err := captureFD(1, &os.Stdout, stdoutWH); err != nil {
		return err
	}
	if err := captureFD(2, &os.Stderr, stderrWH); err != nil {
		return err
	}

	reg.Register(stdoutWH)
	reg.Register(stderrWH)
	return nil
}

// captureFD redirects the given file descriptor (1=stdout, 2=stderr) through
// a pipe. A goroutine reads the pipe and writes to both the original fd (so
// terminal output still works) and the wormhole broadcaster.
func captureFD(fd int, goFile **os.File, wh *Wormhole) error {
	// Save original fd
	origFD, err := syscall.Dup(fd)
	if err != nil {
		return err
	}
	origFile := os.NewFile(uintptr(origFD), "orig")

	// Create pipe
	pr, pw, err := os.Pipe()
	if err != nil {
		origFile.Close()
		return err
	}

	// Redirect fd to pipe write end
	if err := syscall.Dup2(int(pw.Fd()), fd); err != nil {
		pr.Close()
		pw.Close()
		origFile.Close()
		return err
	}

	// Update Go's os.Stdout / os.Stderr
	*goFile = os.NewFile(uintptr(fd), pw.Name())

	// Close our copy of the write end; fd itself now owns it
	pw.Close()

	// Fan out: read pipe, write to original terminal + wormhole
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := pr.Read(buf)
			if n > 0 {
				chunk := buf[:n]
				origFile.Write(chunk)
				wh.Write(chunk)
			}
			if err != nil {
				break
			}
		}
	}()

	return nil
}

// WriterFor returns an io.Writer that writes to both the wormhole and an
// optional additional writer (e.g., the original terminal). Useful for
// creating writers that log to a wormhole.
func WriterFor(wh *Wormhole, also io.Writer) io.Writer {
	if also == nil {
		return wh
	}
	return io.MultiWriter(wh, also)
}
