package hub

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHub_SubscribeReturnsChannel(t *testing.T) {
	t.Parallel()
	h := NewHub()
	defer h.Close()
	s := h.Subscribe()
	if s == nil {
		t.Fatal("Subscribe returned nil")
	}
	if s.C == nil {
		t.Fatal("Subscriber channel is nil")
	}
	if h.SubscriberCount() != 1 {
		t.Errorf("SubscriberCount: got %d, want 1", h.SubscriberCount())
	}
}

func TestHub_PublishFanout(t *testing.T) {
	t.Parallel()
	h := NewHub()
	defer h.Close()
	s1 := h.Subscribe()
	s2 := h.Subscribe()
	defer h.Unsubscribe(s1)
	defer h.Unsubscribe(s2)
	h.Publish(Event{Type: "test", Data: "x"})
	for i, s := range []*Subscriber{s1, s2} {
		select {
		case ev := <-s.C:
			if ev.Type != "test" {
				t.Errorf("sub %d: type %q", i, ev.Type)
			}
		case <-time.After(time.Second):
			t.Errorf("sub %d: no event received", i)
		}
	}
}

func TestHub_PublishNoSubscribersIsNoop(t *testing.T) {
	t.Parallel()
	h := NewHub()
	defer h.Close()
	// Should not panic or block.
	h.Publish(Event{Type: "nobody", Data: 1})
}

func TestHub_UnsubscribeClosesChannel(t *testing.T) {
	t.Parallel()
	h := NewHub()
	defer h.Close()
	s := h.Subscribe()
	if h.SubscriberCount() != 1 {
		t.Errorf("before unsubscribe: count %d", h.SubscriberCount())
	}
	h.Unsubscribe(s)
	if h.SubscriberCount() != 0 {
		t.Errorf("after unsubscribe: count %d", h.SubscriberCount())
	}
	// Reading from closed channel should yield zero value and ok=false.
	select {
	case _, ok := <-s.C:
		if ok {
			t.Errorf("expected closed channel")
		}
	case <-time.After(time.Second):
		t.Errorf("unsubscribe did not close channel")
	}
}

func TestHub_UnsubscribeIdempotent(t *testing.T) {
	t.Parallel()
	h := NewHub()
	defer h.Close()
	s := h.Subscribe()
	h.Unsubscribe(s)
	// Second unsubscribe on already-removed sub should not panic
	// (or close the channel twice). Channel should be closed.
	h.Unsubscribe(s)
}

func TestHub_CloseClosesAllSubscribers(t *testing.T) {
	t.Parallel()
	h := NewHub()
	s1 := h.Subscribe()
	s2 := h.Subscribe()
	if h.SubscriberCount() != 2 {
		t.Errorf("count: %d", h.SubscriberCount())
	}
	h.Close()
	if h.SubscriberCount() != 0 {
		t.Errorf("after close: count %d", h.SubscriberCount())
	}
	for i, s := range []*Subscriber{s1, s2} {
		select {
		case _, ok := <-s.C:
			if ok {
				t.Errorf("sub %d: expected closed channel", i)
			}
		case <-time.After(time.Second):
			t.Errorf("sub %d: timeout waiting for close", i)
		}
	}
}

func TestHub_CloseIdempotent(t *testing.T) {
	t.Parallel()
	h := NewHub()
	h.Close()
	// Second close should not panic.
	h.Close()
}

func TestHub_PublishAfterCloseIsNoop(t *testing.T) {
	t.Parallel()
	h := NewHub()
	h.Close()
	// Should not panic.
	h.Publish(Event{Type: "after-close", Data: 1})
}

func TestHub_SubscribeAfterCloseReturnsClosed(t *testing.T) {
	t.Parallel()
	h := NewHub()
	h.Close()
	s := h.Subscribe()
	if s == nil {
		t.Fatal("Subscribe after Close returned nil")
	}
	// Channel should be immediately closed.
	select {
	case _, ok := <-s.C:
		if ok {
			t.Errorf("expected closed channel after Subscribe on closed hub")
		}
	case <-time.After(time.Second):
		t.Errorf("channel not closed after Subscribe on closed hub")
	}
}

func TestHub_PublishDropsOnFullChannel(t *testing.T) {
	t.Parallel()
	h := NewHub()
	defer h.Close()
	s := h.Subscribe()
	defer h.Unsubscribe(s)
	// Don't drain the channel. Publish > 64 events. The publisher
	// must drop, not block. (Subscribe's channel is buffered 64.)
	for i := 0; i < 200; i++ {
		h.Publish(Event{Type: "drop", Data: i})
	}
	// Drain whatever made it through — should be ≤ 64.
	count := 0
	timeout := time.After(100 * time.Millisecond)
loop:
	for {
		select {
		case _, ok := <-s.C:
			if !ok {
				break loop
			}
			count++
		case <-timeout:
			break loop
		}
	}
	if count > 64 {
		t.Errorf("got %d events, expected ≤ 64 (channel cap)", count)
	}
	// We should have at least some events.
	if count == 0 {
		t.Errorf("got 0 events; expected some to land before the cap")
	}
}

func TestHub_ConcurrentPublishAndSubscribe(t *testing.T) {
	t.Parallel()
	h := NewHub()
	defer h.Close()
	const n = 100
	var wg sync.WaitGroup
	var published atomic.Int64
	// 4 publishers, 4 subscribers, all running for n events each.
	for p := 0; p < 4; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < n; i++ {
				h.Publish(Event{Type: "race", Data: i})
				published.Add(1)
			}
		}()
	}
	subs := make([]*Subscriber, 4)
	for i := range subs {
		subs[i] = h.Subscribe()
	}
	// Let publishers run, then drain.
	time.Sleep(50 * time.Millisecond)
	for _, s := range subs {
		h.Unsubscribe(s)
	}
	wg.Wait()
	if got := published.Load(); got != 4*n {
		t.Errorf("published count: got %d, want %d", got, 4*n)
	}
}

func TestHub_UnsubscribeUnknownDoesNotPanic(t *testing.T) {
	t.Parallel()
	h := NewHub()
	defer h.Close()
	// Random subscriber that was never registered.
	unknown := &Subscriber{C: make(chan Event, 1)}
	h.Unsubscribe(unknown) // should be a no-op
}
