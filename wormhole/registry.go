package wormhole

import (
	"sort"
	"sync"
)

// Registry holds all available wormholes by kind.
type Registry struct {
	mu     sync.RWMutex
	holes  map[Kind]*Wormhole
	hidden map[Kind]bool

	// Command is the command wormhole, if enabled. Stored separately
	// because it has its own handler and auth flow.
	Command *CommandWormhole
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		holes:  make(map[Kind]*Wormhole),
		hidden: make(map[Kind]bool),
	}
}

// Register adds a wormhole to the registry.
func (r *Registry) Register(wh *Wormhole) {
	r.mu.Lock()
	r.holes[wh.kind] = wh
	r.mu.Unlock()
}

// RegisterHidden adds a wormhole that is accessible by kind but hidden
// from the discovery page (Kinds/All).
func (r *Registry) RegisterHidden(wh *Wormhole) {
	r.mu.Lock()
	r.holes[wh.kind] = wh
	r.hidden[wh.kind] = true
	r.mu.Unlock()
}

// Get returns the wormhole for the given kind, or nil.
func (r *Registry) Get(kind Kind) *Wormhole {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.holes[kind]
}

// Kinds returns all registered kinds in sorted order.
func (r *Registry) Kinds() []Kind {
	r.mu.RLock()
	defer r.mu.RUnlock()
	kinds := make([]Kind, 0, len(r.holes))
	for k := range r.holes {
		if r.hidden[k] {
			continue
		}
		kinds = append(kinds, k)
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	return kinds
}

// All returns all registered wormholes.
func (r *Registry) All() []*Wormhole {
	kinds := r.Kinds()
	r.mu.RLock()
	defer r.mu.RUnlock()
	whs := make([]*Wormhole, len(kinds))
	for i, k := range kinds {
		whs[i] = r.holes[k]
	}
	return whs
}
