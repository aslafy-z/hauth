package adapter

import (
	"fmt"
	"sort"
	"sync"
)

var (
	mu       sync.RWMutex
	registry = map[string]Adapter{}
)

// Register adds an adapter. It panics on duplicate names; registration
// happens from init or explicit wiring, both programmer-controlled.
func Register(a Adapter) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := registry[a.Name()]; dup {
		panic(fmt.Sprintf("adapter %q registered twice", a.Name()))
	}
	registry[a.Name()] = a
}

// Get returns the adapter for name.
func Get(name string) (Adapter, bool) {
	mu.RLock()
	defer mu.RUnlock()
	a, ok := registry[name]
	return a, ok
}

// All returns registered adapters sorted by name.
func All() []Adapter {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Adapter, 0, len(registry))
	for _, a := range registry {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}
