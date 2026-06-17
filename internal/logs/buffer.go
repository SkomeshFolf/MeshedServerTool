// Package logs provides in-memory log line buffers with a subscriber API.
//
// Each managed server has its own Buffer. Producers (subprocess stdout/
// stderr pumps) call Append; consumers (API handlers, WebSocket subscribers)
// call Subscribe to receive new lines, and Snapshot to fetch the recent
// history (for late joiners).
package logs

import (
	"sync"
	"time"
)

// Line is one log entry.
type Line struct {
	// When the line was added to the buffer. Approximate; we set it at
	// Append time, not parse time, so it always reflects "when did the
	// manager see this line", which is what dashboards want.
	At time.Time `json:"at"`
	// "stdout" or "stderr".
	Stream string `json:"stream"`
	// The line text, with the trailing newline stripped.
	Text string `json:"text"`
	// Parsed event type, if any. Empty for unparseable lines.
	Type string `json:"type,omitempty"`
	// Optional structured fields extracted by the parser.
	Fields map[string]any `json:"fields,omitempty"`
}

// Buffer is a ring of recent Lines with a fan-out subscriber list.
// Safe for concurrent use.
type Buffer struct {
	mu          sync.RWMutex
	lines       []Line
	capacity    int
	subscribers map[chan Line]struct{}
}

// NewBuffer returns a Buffer that retains the last `capacity` lines.
func NewBuffer(capacity int) *Buffer {
	if capacity <= 0 {
		capacity = 100
	}
	return &Buffer{
		lines:       make([]Line, 0, capacity),
		capacity:    capacity,
		subscribers: make(map[chan Line]struct{}),
	}
}

// Append adds a line to the buffer and broadcasts to subscribers.
//
// If the buffer is full, the oldest line is dropped.
func (b *Buffer) Append(l Line) {
	if l.At.IsZero() {
		l.At = time.Now().UTC()
	}
	if l.Type == "" {
		l.Type, l.Fields = ParseLine(l.Text)
	}

	b.mu.Lock()
	if len(b.lines) >= b.capacity {
		// Drop the oldest.
		b.lines = append(b.lines[:0], b.lines[1:]...)
		// Above trims the underlying array? No — copy last (cap-1) lines
		// into a fresh slice. Cheaper: use a circular index.
	}
	b.lines = append(b.lines, l)
	// Copy subscriber list under lock so we don't hold it during sends.
	subs := make([]chan Line, 0, len(b.subscribers))
	for c := range b.subscribers {
		subs = append(subs, c)
	}
	b.mu.Unlock()

	for _, c := range subs {
		// Non-blocking send: if a subscriber is slow, drop the line for them.
		// They can recover via Snapshot.
		select {
		case c <- l:
		default:
		}
	}
}

// Snapshot returns the last n lines (or all of them if n <= 0).
// Returns a copy; the caller is free to mutate the slice.
func (b *Buffer) Snapshot(n int) []Line {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if n <= 0 || n > len(b.lines) {
		n = len(b.lines)
	}
	out := make([]Line, n)
	copy(out, b.lines[len(b.lines)-n:])
	return out
}

// Subscribe returns a channel that receives new lines until Unsubscribe.
// Buffer size is small; the manager will drop on overflow rather than block.
func (b *Buffer) Subscribe() chan Line {
	ch := make(chan Line, 64)
	b.mu.Lock()
	b.subscribers[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber and closes its channel.
func (b *Buffer) Unsubscribe(ch chan Line) {
	b.mu.Lock()
	if _, ok := b.subscribers[ch]; ok {
		delete(b.subscribers, ch)
		close(ch)
	}
	b.mu.Unlock()
}
