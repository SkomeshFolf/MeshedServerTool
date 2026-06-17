// Package hub implements a publish/subscribe event bus for the v3 backend.
//
// Producers (the server manager, the log buffers) call Publish; consumers
// (WebSocket clients) call Subscribe to receive a stream of events.
//
// The hub is fan-out: every subscriber gets every event. Filtering (e.g.
// "only logs for server X") is the consumer's job — keeps the hub
// implementation simple and lets clients do smarter filtering in
// user-space.
package hub

import (
	"sync"
	"sync/atomic"
)

// Event is a single message flowing through the hub. Type is the routing
// key (e.g. "server.state", "log.line"); Data is opaque JSON.
type Event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// Subscriber is a channel-based consumer.
type Subscriber struct {
	C chan Event
}

// Hub is the central event bus.
type Hub struct {
	mu     sync.RWMutex
	subs   map[*Subscriber]struct{}
	closed atomic.Bool
}

// NewHub returns a new Hub.
func NewHub() *Hub {
	return &Hub{
		subs: make(map[*Subscriber]struct{}),
	}
}

// Subscribe registers a new subscriber. The returned Subscriber's C channel
// is buffered (size 64); publishers drop on overflow rather than blocking.
//
// The caller must Unsubscribe when done to release resources.
func (h *Hub) Subscribe() *Subscriber {
	s := &Subscriber{C: make(chan Event, 64)}
	h.mu.Lock()
	if h.closed.Load() {
		h.mu.Unlock()
		// Closed hubs get an immediately-closed channel so consumers
		// see the EOF right away.
		close(s.C)
		return s
	}
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s
}

// SubscriberCount returns the number of active subscribers. Used for
// diagnostics; not part of the contract.
func (h *Hub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs)
}

// Unsubscribe removes a subscriber and closes its channel.
func (h *Hub) Unsubscribe(s *Subscriber) {
	h.mu.Lock()
	if _, ok := h.subs[s]; ok {
		delete(h.subs, s)
		close(s.C)
	}
	h.mu.Unlock()
}

// Publish fans an event out to all subscribers. Slow subscribers drop the
// event (their channel is full); we never block publishers.
func (h *Hub) Publish(ev Event) {
	if h.closed.Load() {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for s := range h.subs {
		select {
		case s.C <- ev:
		default:
			// Subscriber is slow. Drop. They can re-fetch state
			// via REST if they need to catch up.
		}
	}
}

// Close shuts the hub down, closing all subscriber channels. Subsequent
// Publish calls are no-ops; Subscribe returns an immediately-closed channel.
func (h *Hub) Close() {
	if !h.closed.CompareAndSwap(false, true) {
		return
	}
	h.mu.Lock()
	for s := range h.subs {
		close(s.C)
		delete(h.subs, s)
	}
	h.mu.Unlock()
}
