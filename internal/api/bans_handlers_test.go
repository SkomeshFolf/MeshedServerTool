package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Skomesh/MeshedServerTool/internal/bans"
	"github.com/Skomesh/MeshedServerTool/internal/hub"
	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

// newTestBansEnv wires a v1BansDeps with a fresh in-memory DB and a
// real (in-process) hub so we can also assert that the handler
// publishes events.
func newTestBansEnv(t *testing.T) *v1BansDeps {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "bans-test.db") +
		"?mode=memory&cache=shared&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	store, err := storage.OpenFromDSN(dsn)
	if err != nil {
		t.Fatalf("storage.OpenFromDSN: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &v1BansDeps{
		store: bans.New(store),
		hub:   hub.NewHub(),
	}
}

// call runs the bans handler with the given request body, returning
// the recorder and a decode helper. body is a JSON-encodable value
// (use nil for no body).
func (d *v1BansDeps) call(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reqBody *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reqBody = bytes.NewReader(raw)
	} else {
		reqBody = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, reqBody)
	rr := httptest.NewRecorder()
	d.ServeHTTP(rr, r)
	return rr
}

func TestBans_List_Empty(t *testing.T) {
	t.Parallel()
	d := newTestBansEnv(t)
	rr := d.call(t, http.MethodGet, "/", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	var resp struct {
		Bans []*bans.Ban `json:"bans"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Bans == nil || len(resp.Bans) != 0 {
		t.Errorf("expected empty array, got %v", resp.Bans)
	}
}

func TestBans_Add_HappyPath(t *testing.T) {
	t.Parallel()
	d := newTestBansEnv(t)
	rr := d.call(t, http.MethodPost, "/", map[string]string{
		"steam_id":    "76561198000000001",
		"player_name": "tester",
		"reason":      "cheating",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var got bans.Ban
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.SteamID != "76561198000000001" {
		t.Errorf("steam_id: got %q, want %q", got.SteamID, "76561198000000001")
	}
	if got.PlayerName != "tester" {
		t.Errorf("player_name: got %q, want %q", got.PlayerName, "tester")
	}
	if got.Reason != "cheating" {
		t.Errorf("reason: got %q, want %q", got.Reason, "cheating")
	}
	if got.ID == 0 {
		t.Errorf("expected non-zero id, got 0")
	}
}

func TestBans_Add_MissingSteamID(t *testing.T) {
	t.Parallel()
	d := newTestBansEnv(t)
	rr := d.call(t, http.MethodPost, "/", map[string]string{
		"player_name": "tester",
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "steam_id") {
		t.Errorf("error body should mention steam_id, got %s", rr.Body.String())
	}
}

func TestBans_Add_InvalidJSON(t *testing.T) {
	t.Parallel()
	d := newTestBansEnv(t)
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{not json"))
	rr := httptest.NewRecorder()
	d.ServeHTTP(rr, r)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rr.Code)
	}
}

func TestBans_Add_DuplicateUpdates(t *testing.T) {
	t.Parallel()
	d := newTestBansEnv(t)
	// First add.
	rr1 := d.call(t, http.MethodPost, "/", map[string]string{
		"steam_id":    "76561198000000001",
		"player_name": "oldname",
		"reason":      "old reason",
	})
	if rr1.Code != http.StatusOK {
		t.Fatalf("first add: %d", rr1.Code)
	}
	var first bans.Ban
	_ = json.NewDecoder(rr1.Body).Decode(&first)
	// Re-add with new name/reason — same SteamID.
	rr2 := d.call(t, http.MethodPost, "/", map[string]string{
		"steam_id":    "76561198000000001",
		"player_name": "newname",
		"reason":      "new reason",
	})
	if rr2.Code != http.StatusOK {
		t.Fatalf("second add: %d", rr2.Code)
	}
	var second bans.Ban
	_ = json.NewDecoder(rr2.Body).Decode(&second)
	// ON CONFLICT keeps id stable.
	if second.ID != first.ID {
		t.Errorf("expected same id on dedup, got %d (was %d)", second.ID, first.ID)
	}
	if second.PlayerName != "newname" {
		t.Errorf("player_name not updated: %q", second.PlayerName)
	}
	// List should contain exactly one entry.
	rrL := d.call(t, http.MethodGet, "/", nil)
	var list struct {
		Bans []*bans.Ban `json:"bans"`
	}
	_ = json.NewDecoder(rrL.Body).Decode(&list)
	if len(list.Bans) != 1 {
		t.Errorf("expected 1 ban after dedup, got %d", len(list.Bans))
	}
}

func TestBans_Add_PublishesHubEvent(t *testing.T) {
	t.Parallel()
	d := newTestBansEnv(t)
	sub := d.hub.Subscribe()
	defer d.hub.Unsubscribe(sub)

	rr := d.call(t, http.MethodPost, "/", map[string]string{
		"steam_id": "76561198000000001",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("add: %d", rr.Code)
	}
	select {
	case ev := <-sub.C:
		if ev.Type != "ban.added" {
			t.Errorf("event type: got %q, want ban.added", ev.Type)
		}
	default:
		t.Errorf("expected ban.added event, got nothing")
	}
}

func TestBans_List_AfterAdds(t *testing.T) {
	t.Parallel()
	d := newTestBansEnv(t)
	for _, id := range []string{"76561198000000001", "76561198000000002", "76561198000000003"} {
		rr := d.call(t, http.MethodPost, "/", map[string]string{"steam_id": id})
		if rr.Code != http.StatusOK {
			t.Fatalf("add %s: %d", id, rr.Code)
		}
	}
	rr := d.call(t, http.MethodGet, "/", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d", rr.Code)
	}
	var resp struct {
		Bans []*bans.Ban `json:"bans"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if len(resp.Bans) != 3 {
		t.Errorf("expected 3 bans, got %d", len(resp.Bans))
	}
}

func TestBans_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	d := newTestBansEnv(t)
	for _, m := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		rr := d.call(t, m, "/", nil)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /: got %d, want 405", m, rr.Code)
		}
	}
}

func TestBans_Remove_HappyPath(t *testing.T) {
	t.Parallel()
	d := newTestBansEnv(t)
	rr := d.call(t, http.MethodPost, "/", map[string]string{"steam_id": "76561198000000001"})
	if rr.Code != http.StatusOK {
		t.Fatalf("add: %d", rr.Code)
	}
	var added bans.Ban
	_ = json.NewDecoder(rr.Body).Decode(&added)

	sub := d.hub.Subscribe()
	defer d.hub.Unsubscribe(sub)

	// DELETE /{id}
	rr = d.call(t, http.MethodDelete, "/"+itoa(added.ID), nil)
	if rr.Code != http.StatusOK {
		t.Errorf("remove: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	// Verify gone.
	rrL := d.call(t, http.MethodGet, "/", nil)
	var list struct {
		Bans []*bans.Ban `json:"bans"`
	}
	_ = json.NewDecoder(rrL.Body).Decode(&list)
	if len(list.Bans) != 0 {
		t.Errorf("expected 0 bans after remove, got %d", len(list.Bans))
	}
	// Verify hub event.
	select {
	case ev := <-sub.C:
		if ev.Type != "ban.removed" {
			t.Errorf("event type: got %q, want ban.removed", ev.Type)
		}
	default:
		t.Errorf("expected ban.removed event, got nothing")
	}
}

func TestBans_Remove_NotFound(t *testing.T) {
	t.Parallel()
	d := newTestBansEnv(t)
	rr := d.call(t, http.MethodDelete, "/9999", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rr.Code)
	}
}

func TestBans_Remove_InvalidID(t *testing.T) {
	t.Parallel()
	d := newTestBansEnv(t)
	rr := d.call(t, http.MethodDelete, "/notanumber", nil)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rr.Code)
	}
}

func TestBans_Remove_WrongMethod(t *testing.T) {
	t.Parallel()
	d := newTestBansEnv(t)
	rr := d.call(t, http.MethodPost, "/1", nil)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", rr.Code)
	}
}

func TestBans_Remove_NestedPath404(t *testing.T) {
	t.Parallel()
	d := newTestBansEnv(t)
	// Path with extra segment beyond the id should 404.
	rr := d.call(t, http.MethodDelete, "/1/extra", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rr.Code)
	}
}

func TestBans_Add_FillsBannedByFromContext(t *testing.T) {
	t.Parallel()
	// Verify the handler defaults BannedBy to the authenticated
	// user's username when the field isn't provided in the body.
	d := newTestBansEnv(t)
	body, _ := json.Marshal(map[string]string{"steam_id": "76561198000000001"})
	r := stubAdminRequest(http.MethodPost, "/")
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	d.ServeHTTP(rr, r)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var got bans.Ban
	_ = json.NewDecoder(rr.Body).Decode(&got)
	if got.BannedBy != "admin" {
		t.Errorf("BannedBy: got %q, want admin", got.BannedBy)
	}
}

// itoa is a tiny helper to avoid importing strconv in every test.
func itoa(n int64) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{digits[n%10]}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}
