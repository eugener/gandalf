// Package provider implements the provider registry for LLM provider adapters.
package provider

import (
	"fmt"
	"slices"
	"sync"

	gateway "github.com/eugener/gandalf/internal"
)

// Registry maps provider names to gateway.Provider instances.
// It is safe for concurrent use.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]gateway.Provider
}

// NewRegistry returns an empty, ready-to-use Registry.
func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]gateway.Provider)}
}

// Register adds a provider under the given name.
// It overwrites any previously registered provider with the same name.
func (r *Registry) Register(name string, p gateway.Provider) {
	r.mu.Lock()
	r.providers[name] = p
	r.mu.Unlock()
}

// Get returns the provider registered under name, or an error if not found.
func (r *Registry) Get(name string) (gateway.Provider, error) {
	r.mu.RLock()
	p, ok := r.providers[name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("provider %q not registered", name)
	}
	return p, nil
}

// List returns a sorted slice of all registered provider names.
func (r *Registry) List() []string {
	r.mu.RLock()
	names := slices.Collect(func(yield func(string) bool) {
		for name := range r.providers {
			if !yield(name) {
				return
			}
		}
	})
	r.mu.RUnlock()
	slices.Sort(names)
	return names
}
