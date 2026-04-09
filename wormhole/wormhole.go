// Package wormhole provides named, fan-out broadcast streams that HTTP clients
// can subscribe to. Each wormhole maintains a ring buffer of recent lines so
// new subscribers get immediate context.
package wormhole

import (
	"bufio"
	"bytes"
	"io"
	"sync"
	"sync/atomic"
)

const defaultRingSize = 1000

// Kind identifies a wormhole stream type.
type Kind string

const (
	KindStdout   Kind = "stdout"
	KindStderr   Kind = "stderr"
	KindRequests Kind = "requests"
	KindBoot     Kind = "boot"
)

// Wormhole is a fan-out broadcaster. Writers push data in; each subscriber
// gets a copy on their own channel.
type Wormhole struct {
	kind Kind
	desc string

	mu   sync.RWMutex
	subs map[uint64]chan []byte
	next uint64

	ring     [][]byte
	ringPos  int
	ringFull bool
	ringSize int

	subCount atomic.Int64
}

// New creates a wormhole with the given kind and description.
func New(kind Kind, description string) *Wormhole {
	return &Wormhole{
		kind:     kind,
		desc:     description,
		subs:     make(map[uint64]chan []byte),
		ringSize: defaultRingSize,
		ring:     make([][]byte, defaultRingSize),
	}
}

// Kind returns the wormhole's kind.
func (w *Wormhole) Kind() Kind { return w.kind }

// Description returns the wormhole's description.
func (w *Wormhole) Description() string { return w.desc }

// Subscribers returns the current subscriber count.
func (w *Wormhole) Subscribers() int64 { return w.subCount.Load() }

// Subscribe returns a channel that receives broadcast data and an unsubscribe
// function. The returned channel has a buffer to absorb short bursts; slow
// consumers that fall behind will have messages dropped.
func (w *Wormhole) Subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 256)
	w.mu.Lock()
	id := w.next
	w.next++
	w.subs[id] = ch
	w.mu.Unlock()
	w.subCount.Add(1)

	unsub := func() {
		w.mu.Lock()
		delete(w.subs, id)
		w.mu.Unlock()
		w.subCount.Add(-1)
	}
	return ch, unsub
}

// Tail returns the last n lines from the ring buffer.
func (w *Wormhole) Tail(n int) [][]byte {
	w.mu.RLock()
	defer w.mu.RUnlock()

	total := w.ringCount()
	if n > total {
		n = total
	}
	if n == 0 {
		return nil
	}

	result := make([][]byte, n)
	start := (w.ringPos - n + w.ringSize) % w.ringSize
	for i := 0; i < n; i++ {
		idx := (start + i) % w.ringSize
		result[i] = w.ring[idx]
	}
	return result
}

func (w *Wormhole) ringCount() int {
	if w.ringFull {
		return w.ringSize
	}
	return w.ringPos
}

// Write implements io.Writer. It splits input into lines, stores them in the
// ring buffer, and broadcasts to all subscribers.
func (w *Wormhole) Write(p []byte) (int, error) {
	n := len(p)
	scanner := bufio.NewScanner(bytes.NewReader(p))
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		line = append(line, '\n')
		w.pushLine(line)
	}
	return n, nil
}

// WriteLineRaw stores a pre-formed line and broadcasts it. The caller is
// responsible for including the trailing newline if desired.
func (w *Wormhole) WriteLineRaw(line []byte) {
	w.pushLine(line)
}

func (w *Wormhole) pushLine(line []byte) {
	w.mu.Lock()
	w.ring[w.ringPos] = line
	w.ringPos = (w.ringPos + 1) % w.ringSize
	if w.ringPos == 0 {
		w.ringFull = true
	}

	// Snapshot subs under lock to avoid holding it during sends
	subs := make([]chan []byte, 0, len(w.subs))
	for _, ch := range w.subs {
		subs = append(subs, ch)
	}
	w.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- line:
		default:
			// Slow consumer, drop
		}
	}
}

// WriteTo replays the ring buffer into the writer.
func (w *Wormhole) WriteTo(dst io.Writer) (int64, error) {
	lines := w.Tail(w.ringSize)
	var total int64
	for _, line := range lines {
		n, err := dst.Write(line)
		total += int64(n)
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
