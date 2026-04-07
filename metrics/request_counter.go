package metrics

import (
	"net/http"
	"sync"
	"sync/atomic"
)

// RequestCounter tracks HTTP request counts by path and status code.
type RequestCounter struct {
	total    atomic.Int64
	byPath   map[string]*atomic.Int64
	byStatus map[int]*atomic.Int64
	mu       sync.RWMutex
}

// NewRequestCounter creates a new counter.
func NewRequestCounter() *RequestCounter {
	return &RequestCounter{
		byPath:   make(map[string]*atomic.Int64),
		byStatus: make(map[int]*atomic.Int64),
	}
}

// Wrap returns middleware that counts requests.
func (rc *RequestCounter) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
