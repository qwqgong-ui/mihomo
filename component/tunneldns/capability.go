// Package tunneldns remembers which proxy servers answer DNS on the reserved
// tunnel destination.
//
// The reserved destination is answered inside the proxy server, so a server
// that has not implemented it simply refuses the question or never accepts the
// connection at all. That costs one attempt to discover; repeating it for
// every query would turn a fallback into a tax. One answer per node is enough,
// and an unsupported verdict expires so a server that has since been upgraded
// is picked up again.
package tunneldns

import (
	"sync"
	"time"
)

// UnsupportedTTL is how long a node that could not answer is left alone.
const UnsupportedTTL = 5 * time.Minute

// Registry records per node whether the reserved destination is served there.
type Registry struct {
	mu    sync.RWMutex
	nodes map[string]time.Time // node -> when an unsupported verdict expires
	now   func() time.Time
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{nodes: make(map[string]time.Time), now: time.Now}
}

var defaultRegistry = NewRegistry()

// Supported reports whether node is worth asking. A node that has never been
// tried is worth asking, so the answer for an unknown node is yes.
func (r *Registry) Supported(node string) bool {
	if node == "" {
		return true
	}
	r.mu.RLock()
	until, known := r.nodes[node]
	r.mu.RUnlock()
	if !known {
		return true
	}
	if r.now().Before(until) {
		return false
	}
	// The verdict has expired: forget it so the node is tried again.
	r.mu.Lock()
	if expiry, still := r.nodes[node]; still && !r.now().Before(expiry) {
		delete(r.nodes, node)
	}
	r.mu.Unlock()
	return true
}

// MarkSupported records that node answered.
func (r *Registry) MarkSupported(node string) {
	if node == "" {
		return
	}
	r.mu.Lock()
	delete(r.nodes, node)
	r.mu.Unlock()
}

// MarkUnsupported records that node could not answer, for UnsupportedTTL.
func (r *Registry) MarkUnsupported(node string) {
	if node == "" {
		return
	}
	r.mu.Lock()
	r.nodes[node] = r.now().Add(UnsupportedTTL)
	r.mu.Unlock()
}

// Reset forgets every verdict. Configuration reloads replace the proxy set, so
// what was learned about the old one no longer applies.
func (r *Registry) Reset() {
	r.mu.Lock()
	clear(r.nodes)
	r.mu.Unlock()
}

// Supported reports whether node is worth asking.
func Supported(node string) bool { return defaultRegistry.Supported(node) }

// MarkSupported records that node answered.
func MarkSupported(node string) { defaultRegistry.MarkSupported(node) }

// MarkUnsupported records that node could not answer, for UnsupportedTTL.
func MarkUnsupported(node string) { defaultRegistry.MarkUnsupported(node) }

// Reset forgets every verdict.
func Reset() { defaultRegistry.Reset() }
