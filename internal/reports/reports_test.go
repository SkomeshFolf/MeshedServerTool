package reports

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "reports-test.db") +
		"?mode=memory&cache=shared&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	s, err := storage.OpenFromDSN(dsn)
	if err != nil {
		t.Fatalf("OpenFromDSN: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return New(s)
}

func makeReport(target, source, reason, text string) *Report {
	return &Report{
		ServerName: "alpha",
		TargetID:   "T1",
		TargetName: target,
		SourceID:   "S1",
		SourceName: source,
		Date:       "2024-01-01",
		Reason:     reason,
		Text:       text,
	}
}

func TestReports_Hash_Deterministic(t *testing.T) {
	t.Parallel()
	a := Hash("alice", "T1", "bob", "S1", "2024-01-01", "cheat", "they're cheating")
	b := Hash("alice", "T1", "bob", "S1", "2024-01-01", "cheat", "they're cheating")
	if a != b {
		t.Errorf("same inputs should produce same hash, got %q vs %q", a, b)
	}
}

func TestReports_Hash_DifferentFieldsProduceDifferentHash(t *testing.T) {
	t.Parallel()
	a := Hash("alice", "T1", "bob", "S1", "2024-01-01", "cheat", "msg")
	cases := []struct {
		name string
		h    string
	}{
		{"diff target", Hash("eve", "T1", "bob", "S1", "2024-01-01", "cheat", "msg")},
		{"diff targetID", Hash("alice", "T2", "bob", "S1", "2024-01-01", "cheat", "msg")},
		{"diff source", Hash("alice", "T1", "carol", "S1", "2024-01-01", "cheat", "msg")},
		{"diff sourceID", Hash("alice", "T1", "bob", "S2", "2024-01-01", "cheat", "msg")},
		{"diff date", Hash("alice", "T1", "bob", "S1", "2024-01-02", "cheat", "msg")},
		{"diff reason", Hash("alice", "T1", "bob", "S1", "2024-01-01", "spam", "msg")},
		{"diff text", Hash("alice", "T1", "bob", "S1", "2024-01-01", "cheat", "other msg")},
	}
	for _, tc := range cases {
		if tc.h == a {
			t.Errorf("%s: hash should differ from base, but both = %q", tc.name, a)
		}
	}
}

func TestReports_Hash_NULBoundarySafe(t *testing.T) {
	t.Parallel()
	// Two inputs that would collide with naive concatenation but
	// differ under NUL-separator joining.
	// ("ab", "c", "d", "e", "f", "g", "h") vs ("a", "bc", "d", "e", "f", "g", "h")
	// With NUL separators, the boundary is unambiguous.
	a := Hash("ab", "c", "d", "e", "f", "g", "h")
	b := Hash("a", "bc", "d", "e", "f", "g", "h")
	if a == b {
		t.Errorf("NUL-boundary collision detected: %q == %q", a, b)
	}
}

func TestReports_CreateReport_HappyPath(t *testing.T) {
	t.Parallel()
	rs := newTestStore(t)
	r := makeReport("alice", "bob", "cheat", "msg")
	got, exists, err := rs.CreateReport(context.Background(), r)
	if err != nil {
		t.Fatalf("CreateReport: %v", err)
	}
	if exists {
		t.Errorf("first create should not be a dedup")
	}
	if got.ID == 0 {
		t.Errorf("expected non-zero id, got 0")
	}
	if got.Hash == "" {
		t.Errorf("hash should be populated")
	}
	if got.Handled {
		t.Errorf("new report should not be handled")
	}
}

func TestReports_CreateReport_Dedup(t *testing.T) {
	t.Parallel()
	rs := newTestStore(t)
	ctx := context.Background()
	first, _, err := rs.CreateReport(ctx, makeReport("alice", "bob", "cheat", "msg"))
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, exists, err := rs.CreateReport(ctx, makeReport("alice", "bob", "cheat", "msg"))
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !exists {
		t.Errorf("expected dedup flag on second create")
	}
	if first.ID != second.ID {
		t.Errorf("dedup should return same id, got %d vs %d", first.ID, second.ID)
	}
}

func TestReports_CreateReport_ExplicitHash(t *testing.T) {
	t.Parallel()
	rs := newTestStore(t)
	// If the caller supplies a hash, the store uses it directly.
	explicitHash := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	r := makeReport("alice", "bob", "cheat", "msg")
	r.Hash = explicitHash
	got, _, err := rs.CreateReport(context.Background(), r)
	if err != nil {
		t.Fatalf("CreateReport: %v", err)
	}
	byHash, err := rs.GetByHash(context.Background(), explicitHash)
	if err != nil {
		t.Fatalf("GetByHash: %v", err)
	}
	if byHash.ID != got.ID {
		t.Errorf("explicit hash should be stored: got id %d, fetched %d", got.ID, byHash.ID)
	}
}

func TestReports_GetByID_HappyPath(t *testing.T) {
	t.Parallel()
	rs := newTestStore(t)
	ctx := context.Background()
	created, _, _ := rs.CreateReport(ctx, makeReport("alice", "bob", "cheat", "msg"))
	got, err := rs.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("id mismatch: %d vs %d", got.ID, created.ID)
	}
	if got.TargetName != "alice" {
		t.Errorf("target_name: %q", got.TargetName)
	}
}

func TestReports_GetByID_NotFound(t *testing.T) {
	t.Parallel()
	rs := newTestStore(t)
	_, err := rs.GetByID(context.Background(), 99999)
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestReports_GetByHash(t *testing.T) {
	t.Parallel()
	rs := newTestStore(t)
	ctx := context.Background()
	r := makeReport("alice", "bob", "cheat", "msg")
	created, _, _ := rs.CreateReport(ctx, r)
	got, err := rs.GetByHash(ctx, created.Hash)
	if err != nil {
		t.Fatalf("GetByHash: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("id mismatch")
	}
}

func TestReports_GetByHash_NotFound(t *testing.T) {
	t.Parallel()
	rs := newTestStore(t)
	_, err := rs.GetByHash(context.Background(), "ffffffffffffffffffffffffffffffff")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestReports_List_All(t *testing.T) {
	t.Parallel()
	rs := newTestStore(t)
	ctx := context.Background()
	for i, name := range []string{"alice", "bob", "carol"} {
		rs.CreateReport(ctx, makeReport(name, "src", "r", "msg-"+strconv.Itoa(i)))
	}
	got, err := rs.List(ctx, nil, "", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("expected 3, got %d", len(got))
	}
}

func TestReports_List_FilterHandled(t *testing.T) {
	t.Parallel()
	rs := newTestStore(t)
	ctx := context.Background()
	first, _, _ := rs.CreateReport(ctx, makeReport("alice", "src", "r", "msg-1"))
	rs.CreateReport(ctx, makeReport("bob", "src", "r", "msg-2"))
	if err := rs.MarkHandled(ctx, first.ID, true); err != nil {
		t.Fatalf("MarkHandled: %v", err)
	}
	// handled=true
	got, _ := rs.List(ctx, boolPtr(true), "", 0)
	if len(got) != 1 {
		t.Errorf("handled=true: got %d, want 1", len(got))
	}
	// handled=false
	got, _ = rs.List(ctx, boolPtr(false), "", 0)
	if len(got) != 1 {
		t.Errorf("handled=false: got %d, want 1", len(got))
	}
}

func TestReports_List_FilterServer(t *testing.T) {
	t.Parallel()
	rs := newTestStore(t)
	ctx := context.Background()
	r1 := makeReport("alice", "src", "r", "t1")
	r1.ServerName = "alpha"
	r2 := makeReport("bob", "src", "r", "t2")
	r2.ServerName = "beta"
	rs.CreateReport(ctx, r1)
	rs.CreateReport(ctx, r2)
	got, _ := rs.List(ctx, nil, "alpha", 0)
	if len(got) != 1 {
		t.Errorf("alpha filter: got %d, want 1", len(got))
	}
	if got[0].ServerName != "alpha" {
		t.Errorf("wrong server: %q", got[0].ServerName)
	}
}

func TestReports_List_RespectsLimit(t *testing.T) {
	t.Parallel()
	rs := newTestStore(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		r := makeReport("alice", "bob", "cheat", "msg-"+strconv.Itoa(i))
		if _, _, err := rs.CreateReport(ctx, r); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	got, _ := rs.List(ctx, nil, "", 2)
	if len(got) != 2 {
		t.Errorf("limit=2: got %d, want 2", len(got))
	}
}

func TestReports_MarkHandled(t *testing.T) {
	t.Parallel()
	rs := newTestStore(t)
	ctx := context.Background()
	created, _, _ := rs.CreateReport(ctx, makeReport("alice", "src", "r", "t"))
	if err := rs.MarkHandled(ctx, created.ID, true); err != nil {
		t.Fatalf("MarkHandled: %v", err)
	}
	got, _ := rs.GetByID(ctx, created.ID)
	if !got.Handled {
		t.Errorf("expected handled=true")
	}
	if got.HandledAt == nil {
		t.Errorf("handled_at should be set")
	}
	// Marking handled again should still be idempotent.
	if err := rs.MarkHandled(ctx, created.ID, true); err != nil {
		t.Fatalf("MarkHandled idempotent: %v", err)
	}
	// Marking unhandled should clear.
	if err := rs.MarkHandled(ctx, created.ID, false); err != nil {
		t.Fatalf("MarkHandled unhandled: %v", err)
	}
	got, _ = rs.GetByID(ctx, created.ID)
	if got.Handled {
		t.Errorf("expected handled=false after unhandle")
	}
}

func TestReports_MarkHandled_NotFound(t *testing.T) {
	t.Parallel()
	rs := newTestStore(t)
	err := rs.MarkHandled(context.Background(), 99999, true)
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestReports_Delete(t *testing.T) {
	t.Parallel()
	rs := newTestStore(t)
	ctx := context.Background()
	created, _, _ := rs.CreateReport(ctx, makeReport("alice", "src", "r", "t"))
	if err := rs.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := rs.GetByID(ctx, created.ID)
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestReports_Delete_NotFound(t *testing.T) {
	t.Parallel()
	rs := newTestStore(t)
	err := rs.Delete(context.Background(), 99999)
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestReports_CountByTarget(t *testing.T) {
	t.Parallel()
	rs := newTestStore(t)
	ctx := context.Background()
	// Two reports for T1, one for T2, one for T3. To avoid
	// hash dedup, vary the text for each report.
	mk := func(target, text string) *Report {
		r := makeReport("a", "src", "r", text)
		r.TargetID = target
		return r
	}
	rs.CreateReport(ctx, mk("T1", "msg-1"))
	rs.CreateReport(ctx, mk("T1", "msg-2"))
	rs.CreateReport(ctx, mk("T2", "msg-3"))
	rs.CreateReport(ctx, mk("T3", "msg-4"))
	counts, err := rs.CountByTarget(ctx)
	if err != nil {
		t.Fatalf("CountByTarget: %v", err)
	}
	if counts["T1"] != 2 {
		t.Errorf("T1: got %d, want 2", counts["T1"])
	}
	if counts["T2"] != 1 {
		t.Errorf("T2: got %d, want 1", counts["T2"])
	}
	if counts["T3"] != 1 {
		t.Errorf("T3: got %d, want 1", counts["T3"])
	}
}

func boolPtr(b bool) *bool { return &b }
