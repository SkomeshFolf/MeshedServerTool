package logs

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBuffer_AppendAndSnapshot(t *testing.T) {
	t.Parallel()
	b := NewBuffer(10)
	for i := 0; i < 3; i++ {
		b.Append(Line{Text: "line"})
	}
	snap := b.Snapshot(0)
	if len(snap) != 3 {
		t.Errorf("Snapshot: got %d, want 3", len(snap))
	}
}

func TestBuffer_SnapshotN(t *testing.T) {
	t.Parallel()
	b := NewBuffer(10)
	for i := 0; i < 5; i++ {
		b.Append(Line{Text: "line"})
	}
	snap := b.Snapshot(2)
	if len(snap) != 2 {
		t.Errorf("Snapshot(2): got %d, want 2", len(snap))
	}
}

func TestBuffer_SnapshotNExceedsLen(t *testing.T) {
	t.Parallel()
	b := NewBuffer(10)
	for i := 0; i < 3; i++ {
		b.Append(Line{Text: "line"})
	}
	snap := b.Snapshot(100) // n > len
	if len(snap) != 3 {
		t.Errorf("Snapshot(100) with 3 lines: got %d, want 3", len(snap))
	}
}

func TestBuffer_OverflowDropsOldest(t *testing.T) {
	t.Parallel()
	b := NewBuffer(3) // capacity 3
	for i := 0; i < 5; i++ {
		b.Append(Line{Text: "line"})
	}
	snap := b.Snapshot(0)
	if len(snap) != 3 {
		t.Errorf("after overflow: len=%d, want 3", len(snap))
	}
}

func TestBuffer_ZeroCapacityUsesDefault(t *testing.T) {
	t.Parallel()
	b := NewBuffer(0)
	if b.capacity != 100 {
		t.Errorf("capacity: got %d, want 100 (default)", b.capacity)
	}
}

func TestBuffer_AppendSetsAtIfZero(t *testing.T) {
	t.Parallel()
	b := NewBuffer(10)
	before := time.Now().UTC()
	b.Append(Line{Text: "line"})
	after := time.Now().UTC()
	snap := b.Snapshot(0)
	if len(snap) != 1 {
		t.Fatalf("len: %d", len(snap))
	}
	if snap[0].At.IsZero() {
		t.Errorf("At should be set when caller leaves it zero")
	}
	if snap[0].At.Before(before) || snap[0].At.After(after) {
		t.Errorf("At %v not in [%v, %v]", snap[0].At, before, after)
	}
}

func TestBuffer_AppendRespectsCallerAt(t *testing.T) {
	t.Parallel()
	b := NewBuffer(10)
	caller := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	b.Append(Line{Text: "line", At: caller})
	snap := b.Snapshot(0)
	if !snap[0].At.Equal(caller) {
		t.Errorf("caller's At should be preserved, got %v", snap[0].At)
	}
}

func TestBuffer_AppendParsesIfTypeEmpty(t *testing.T) {
	t.Parallel()
	b := NewBuffer(10)
	// A line the parser recognizes (player_join_steamid).
	b.Append(Line{Text: "Player 'TestUser' (76561198000000001) joined the server"})
	snap := b.Snapshot(0)
	if snap[0].Type == "" {
		t.Errorf("expected parser to set Type, got empty")
	}
	if snap[0].Type != "player_join_steamid" {
		t.Errorf("Type: got %q, want player_join_steamid", snap[0].Type)
	}
	if snap[0].Fields["name"] != "TestUser" {
		t.Errorf("name field: %v", snap[0].Fields["name"])
	}
}

func TestBuffer_SubscribeReceivesAppends(t *testing.T) {
	t.Parallel()
	b := NewBuffer(10)
	ch := b.Subscribe()
	defer b.Unsubscribe(ch)
	b.Append(Line{Text: "hello"})
	select {
	case got := <-ch:
		if got.Text != "hello" {
			t.Errorf("text: %q", got.Text)
		}
	case <-time.After(time.Second):
		t.Errorf("timeout waiting for line")
	}
}

func TestBuffer_UnsubscribeClosesChannel(t *testing.T) {
	t.Parallel()
	b := NewBuffer(10)
	ch := b.Subscribe()
	b.Unsubscribe(ch)
	select {
	case _, ok := <-ch:
		if ok {
			t.Errorf("expected closed channel")
		}
	case <-time.After(time.Second):
		t.Errorf("unsubscribe did not close channel")
	}
}

func TestBuffer_UnsubscribeUnknownNoOp(t *testing.T) {
	t.Parallel()
	b := NewBuffer(10)
	// Should not panic.
	b.Unsubscribe(make(chan Line))
}

func TestBuffer_SubscribeNonBlocking(t *testing.T) {
	t.Parallel()
	b := NewBuffer(10)
	ch := b.Subscribe()
	defer b.Unsubscribe(ch)
	// Don't drain. Publish 200 events. None should block.
	for i := 0; i < 200; i++ {
		b.Append(Line{Text: "flood"})
	}
	// Drain whatever made it (channel cap is 64).
	count := 0
	timeout := time.After(100 * time.Millisecond)
loop:
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				break loop
			}
			count++
		case <-timeout:
			break loop
		}
	}
	if count > 64 {
		t.Errorf("got %d events, expected ≤ 64", count)
	}
}

func TestBuffer_OnAppendFires(t *testing.T) {
	t.Parallel()
	b := NewBuffer(10)
	var got []Line
	var mu sync.Mutex
	b.OnAppend(func(l Line) {
		mu.Lock()
		got = append(got, l)
		mu.Unlock()
	})
	for i := 0; i < 3; i++ {
		b.Append(Line{Text: "x"})
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 3 {
		t.Errorf("OnAppend fired %d times, want 3", len(got))
	}
}

func TestBuffer_OnAppendMultiple(t *testing.T) {
	t.Parallel()
	b := NewBuffer(10)
	var a, c atomic.Int32
	b.OnAppend(func(l Line) { a.Add(1) })
	b.OnAppend(func(l Line) { c.Add(1) })
	b.Append(Line{Text: "x"})
	if a.Load() != 1 || c.Load() != 1 {
		t.Errorf("a=%d c=%d, want both 1", a.Load(), c.Load())
	}
}

func TestBuffer_SnapshotReturnsCopy(t *testing.T) {
	t.Parallel()
	b := NewBuffer(10)
	b.Append(Line{Text: "orig"})
	snap := b.Snapshot(1)
	snap[0].Text = "mutated"
	snap2 := b.Snapshot(1)
	if snap2[0].Text != "orig" {
		t.Errorf("Snapshot should return a copy; underlying mutated: %q", snap2[0].Text)
	}
}

func TestBuffer_ConcurrentAppendNoRace(t *testing.T) {
	t.Parallel()
	b := NewBuffer(1000)
	var wg sync.WaitGroup
	const perGoroutine = 100
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				b.Append(Line{Text: "x"})
			}
		}()
	}
	wg.Wait()
	// Capacity is 1000, total appended is 8*100=800. All 800
	// should fit (no overflow). The test's value is running
	// under -race to detect data races.
	snap := b.Snapshot(0)
	if len(snap) != 8*perGoroutine {
		t.Errorf("len=%d, want %d", len(snap), 8*perGoroutine)
	}
}
