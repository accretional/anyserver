// Package metrics provides HTTP request counting middleware and Go runtime
// statistics collection for server observability.
package metrics

import (
	"runtime"
)

// RuntimeStats returns current Go runtime statistics.
type RuntimeStats struct {
	Goroutines    int64
	HeapAllocBytes int64
	SysBytes      int64
	NumGC         int64
}

// GetRuntimeStats reads current Go runtime metrics.
func GetRuntimeStats() RuntimeStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return RuntimeStats{
		Goroutines:    int64(runtime.NumGoroutine()),
		HeapAllocBytes: int64(m.HeapAlloc),
		SysBytes:      int64(m.Sys),
		NumGC:         int64(m.NumGC),
	}
}

// TODO after compaction: add procfs reading for Linux systems
// - /proc/self/stat for CPU time
// - /proc/self/fd for open file descriptor count
// - /proc/self/status for VmRSS, threads
// - Fall back to runtime stats on non-Linux (Darwin, etc.)
