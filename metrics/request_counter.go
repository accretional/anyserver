package metrics

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// RequestCounter tracks HTTP request counts by path and status code.
type RequestCounter struct {
	total    atomic.Int64
	byPath   map[string]*atomic.Int64
	byStatus map[int]*atomic.Int64
	mu       sync.RWMutex
	stream   io.Writer // optional: live request log (wormhole)
}

// NewRequestCounter creates a new counter.
func NewRequestCounter() *RequestCounter {
	return &RequestCounter{
		byPath:   make(map[string]*atomic.Int64),
		byStatus: make(map[int]*atomic.Int64),
	}
}

// SetStream sets an io.Writer that receives a line for every HTTP request.
func (rc *RequestCounter) SetStream(w io.Writer) {
	rc.stream = w
}

// Wrap returns middleware that counts requests.
func (rc *RequestCounter) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)

		rc.total.Add(1)

		rc.mu.Lock()
		if _, ok := rc.byPath[r.URL.Path]; !ok {
			rc.byPath[r.URL.Path] = &atomic.Int64{}
		}
		rc.byPath[r.URL.Path].Add(1)

		if _, ok := rc.byStatus[sw.status]; !ok {
			rc.byStatus[sw.status] = &atomic.Int64{}
		}
		rc.byStatus[sw.status].Add(1)
		rc.mu.Unlock()

		if rc.stream != nil {
			fmt.Fprintf(rc.stream, "%s %s %d %s\n",
				r.Method, r.URL.Path, sw.status, time.Since(start).Truncate(time.Microsecond))
		}
	})
}

// Total returns total request count.
func (rc *RequestCounter) Total() int64 { return rc.total.Load() }

// ByPath returns request counts per path.
func (rc *RequestCounter) ByPath() map[string]int64 {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	out := make(map[string]int64, len(rc.byPath))
	for k, v := range rc.byPath {
		out[k] = v.Load()
	}
	return out
}

// ByStatus returns request counts per status code.
func (rc *RequestCounter) ByStatus() map[int32]int64 {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	out := make(map[int32]int64, len(rc.byStatus))
	for k, v := range rc.byStatus {
		out[int32(k)] = v.Load()
	}
	return out
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

// Flush passes through to the underlying ResponseWriter if it supports flushing.
func (sw *statusWriter) Flush() {
	if f, ok := sw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack passes through to the underlying ResponseWriter for WebSocket upgrades.
func (sw *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := sw.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not support hijacking")
}

// Unwrap exposes the underlying ResponseWriter for interface assertions.
func (sw *statusWriter) Unwrap() http.ResponseWriter {
	return sw.ResponseWriter
}
