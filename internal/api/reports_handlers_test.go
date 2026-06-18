package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Skomesh/MeshedServerTool/internal/hub"
	"github.com/Skomesh/MeshedServerTool/internal/reports"
	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

func newTestReportsEnv(t *testing.T) *v1ReportsDeps {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "reports-test.db") +
		"?mode=memory&cache=shared&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	store, err := storage.OpenFromDSN(dsn)
	if err != nil {
		t.Fatalf("OpenFromDSN: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &v1ReportsDeps{store: reports.New(store), hub: hub.NewHub()}
}

func (d *v1ReportsDeps) callReports(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reqBody *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reqBody = bytes.NewReader(raw)
	} else {
		reqBody = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, reqBody)
	rr := httptest.NewRecorder()
	d.ServeHTTP(rr, r)
	return rr
}

func makeReport(server, target, source, reason string) map[string]string {
	return map[string]string{
		"server_name": server,
		"target_id":   target,
		"target_name": "Target",
		"source_id":   source,
		"source_name": "Source",
		"date":        "2024-01-01",
		"reason":      reason,
		"text":        "Some long report text.",
	}
}

func TestReports_Create_HappyPath(t *testing.T) {
	t.Parallel()
	d := newTestReportsEnv(t)
	sub := d.hub.Subscribe()
	defer d.hub.Unsubscribe(sub)
	rr := d.callReports(t, http.MethodPost, "/", makeReport("alpha", "T1", "S1", "cheat"))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var got reports.Report
	_ = json.NewDecoder(rr.Body).Decode(&got)
	if got.ID == 0 {
		t.Errorf("expected id, got 0")
	}
	select {
	case ev := <-sub.C:
		if ev.Type != "report.new" {
			t.Errorf("event type: %q", ev.Type)
		}
	default:
		t.Errorf("expected report.new event")
	}
}

func TestReports_Create_MissingRequired(t *testing.T) {
	t.Parallel()
	d := newTestReportsEnv(t)
	rr := d.callReports(t, http.MethodPost, "/", map[string]string{
		"server_name": "alpha",
		// target_id and source_id missing
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "required") {
		t.Errorf("error body should say 'required': %s", rr.Body.String())
	}
}

func TestReports_Create_InvalidJSON(t *testing.T) {
	t.Parallel()
	d := newTestReportsEnv(t)
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{not json"))
	rr := httptest.NewRecorder()
	d.ServeHTTP(rr, r)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rr.Code)
	}
}

func TestReports_Create_DedupReturnsExisting(t *testing.T) {
	t.Parallel()
	d := newTestReportsEnv(t)
	body := makeReport("alpha", "T1", "S1", "cheat")
	rr1 := d.callReports(t, http.MethodPost, "/", body)
	if rr1.Code != http.StatusCreated {
		t.Fatalf("first: %d", rr1.Code)
	}
	var first reports.Report
	_ = json.NewDecoder(rr1.Body).Decode(&first)
	// Re-post identical content — the hash is the same, so
	// dedup should kick in and return 200 (not 201).
	rr2 := d.callReports(t, http.MethodPost, "/", body)
	if rr2.Code != http.StatusOK {
		t.Errorf("dedup: got %d, want 200 (de-dup contract)", rr2.Code)
	}
	var second reports.Report
	_ = json.NewDecoder(rr2.Body).Decode(&second)
	if second.ID != first.ID {
		t.Errorf("dedup should return same id, got %d (was %d)", second.ID, first.ID)
	}
}

func TestReports_Get_HappyPath(t *testing.T) {
	t.Parallel()
	d := newTestReportsEnv(t)
	rr := d.callReports(t, http.MethodPost, "/", makeReport("alpha", "T1", "S1", "cheat"))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d", rr.Code)
	}
	var created reports.Report
	_ = json.NewDecoder(rr.Body).Decode(&created)
	rr = d.callReports(t, http.MethodGet, "/"+itoa(created.ID), nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get: %d", rr.Code)
	}
	var got reports.Report
	_ = json.NewDecoder(rr.Body).Decode(&got)
	if got.ID != created.ID {
		t.Errorf("id mismatch: %d vs %d", got.ID, created.ID)
	}
}

func TestReports_Get_NotFound(t *testing.T) {
	t.Parallel()
	d := newTestReportsEnv(t)
	rr := d.callReports(t, http.MethodGet, "/9999", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rr.Code)
	}
}

func TestReports_Get_InvalidID(t *testing.T) {
	t.Parallel()
	d := newTestReportsEnv(t)
	rr := d.callReports(t, http.MethodGet, "/notnum", nil)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rr.Code)
	}
}

func TestReports_Update_MarkHandled(t *testing.T) {
	t.Parallel()
	d := newTestReportsEnv(t)
	sub := d.hub.Subscribe()
	defer d.hub.Unsubscribe(sub)
	rr := d.callReports(t, http.MethodPost, "/", makeReport("alpha", "T1", "S1", "cheat"))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d", rr.Code)
	}
	var created reports.Report
	_ = json.NewDecoder(rr.Body).Decode(&created)
	rr = d.callReports(t, http.MethodPatch, "/"+itoa(created.ID), map[string]bool{"handled": true})
	if rr.Code != http.StatusOK {
		t.Fatalf("update: %d", rr.Code)
	}
	// Verify by GET.
	rr = d.callReports(t, http.MethodGet, "/"+itoa(created.ID), nil)
	var after reports.Report
	_ = json.NewDecoder(rr.Body).Decode(&after)
	if !after.Handled {
		t.Errorf("expected handled=true, got false")
	}
	// Hub event: report.new, then report.updated.
	got := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case ev := <-sub.C:
			got[ev.Type] = true
		default:
		}
	}
	if !got["report.updated"] {
		t.Errorf("missing report.updated event, got %v", got)
	}
}

func TestReports_Update_MissingHandledField(t *testing.T) {
	t.Parallel()
	d := newTestReportsEnv(t)
	rr := d.callReports(t, http.MethodPatch, "/1", map[string]any{})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rr.Code)
	}
}

func TestReports_Update_NotFound(t *testing.T) {
	t.Parallel()
	d := newTestReportsEnv(t)
	rr := d.callReports(t, http.MethodPatch, "/9999", map[string]bool{"handled": true})
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rr.Code)
	}
}

func TestReports_Delete_HappyPath(t *testing.T) {
	t.Parallel()
	d := newTestReportsEnv(t)
	sub := d.hub.Subscribe()
	defer d.hub.Unsubscribe(sub)
	rr := d.callReports(t, http.MethodPost, "/", makeReport("alpha", "T1", "S1", "cheat"))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d", rr.Code)
	}
	var created reports.Report
	_ = json.NewDecoder(rr.Body).Decode(&created)
	rr = d.callReports(t, http.MethodDelete, "/"+itoa(created.ID), nil)
	if rr.Code != http.StatusOK {
		t.Errorf("delete: %d", rr.Code)
	}
	// GET should 404 now.
	rr = d.callReports(t, http.MethodGet, "/"+itoa(created.ID), nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", rr.Code)
	}
	// Hub events: report.new, then report.deleted.
	got := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case ev := <-sub.C:
			got[ev.Type] = true
		default:
		}
	}
	if !got["report.new"] {
		t.Errorf("missing report.new event")
	}
	if !got["report.deleted"] {
		t.Errorf("missing report.deleted event")
	}
}

func TestReports_Delete_NotFound(t *testing.T) {
	t.Parallel()
	d := newTestReportsEnv(t)
	rr := d.callReports(t, http.MethodDelete, "/9999", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rr.Code)
	}
}

func TestReports_List_FilterHandled(t *testing.T) {
	t.Parallel()
	d := newTestReportsEnv(t)
	// Two reports, mark one handled.
	rr1 := d.callReports(t, http.MethodPost, "/", makeReport("alpha", "T1", "S1", "cheat"))
	if rr1.Code != http.StatusCreated {
		t.Fatalf("create 1: %d", rr1.Code)
	}
	rr2 := d.callReports(t, http.MethodPost, "/", makeReport("alpha", "T2", "S2", "spam"))
	if rr2.Code != http.StatusCreated {
		t.Fatalf("create 2: %d", rr2.Code)
	}
	var r1 reports.Report
	_ = json.NewDecoder(rr1.Body).Decode(&r1)
	if rr := d.callReports(t, http.MethodPatch, "/"+itoa(r1.ID), map[string]bool{"handled": true}); rr.Code != http.StatusOK {
		t.Fatalf("mark handled: %d", rr.Code)
	}
	// Filter handled=true.
	rr := d.callReports(t, http.MethodGet, "/?handled=true", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d", rr.Code)
	}
	var resp struct {
		Reports []*reports.Report `json:"reports"`
		PerUser map[string]int    `json:"reports_per_user"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if len(resp.Reports) != 1 {
		t.Errorf("expected 1 handled report, got %d", len(resp.Reports))
	}
	if resp.Reports[0].ID != r1.ID {
		t.Errorf("wrong report id: %d", resp.Reports[0].ID)
	}
	// Filter handled=false.
	rr = d.callReports(t, http.MethodGet, "/?handled=false", nil)
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if len(resp.Reports) != 1 {
		t.Errorf("expected 1 unhandled report, got %d", len(resp.Reports))
	}
}

func TestReports_List_FilterServer(t *testing.T) {
	t.Parallel()
	d := newTestReportsEnv(t)
	if rr := d.callReports(t, http.MethodPost, "/", makeReport("alpha", "T1", "S1", "cheat")); rr.Code != http.StatusCreated {
		t.Fatalf("create: %d", rr.Code)
	}
	if rr := d.callReports(t, http.MethodPost, "/", makeReport("beta", "T2", "S2", "spam")); rr.Code != http.StatusCreated {
		t.Fatalf("create: %d", rr.Code)
	}
	rr := d.callReports(t, http.MethodGet, "/?server=alpha", nil)
	var resp struct {
		Reports []*reports.Report `json:"reports"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if len(resp.Reports) != 1 {
		t.Errorf("expected 1 alpha report, got %d", len(resp.Reports))
	}
	if resp.Reports[0].ServerName != "alpha" {
		t.Errorf("wrong server: %s", resp.Reports[0].ServerName)
	}
}

func TestReports_List_CountByTarget(t *testing.T) {
	t.Parallel()
	d := newTestReportsEnv(t)
	// Two reports for the same target, one for a different target.
	if rr := d.callReports(t, http.MethodPost, "/", makeReport("alpha", "T1", "S1", "cheat")); rr.Code != http.StatusCreated {
		t.Fatalf("create: %d", rr.Code)
	}
	if rr := d.callReports(t, http.MethodPost, "/", makeReport("alpha", "T1", "S2", "spam")); rr.Code != http.StatusCreated {
		t.Fatalf("create: %d", rr.Code)
	}
	if rr := d.callReports(t, http.MethodPost, "/", makeReport("alpha", "T2", "S3", "grief")); rr.Code != http.StatusCreated {
		t.Fatalf("create: %d", rr.Code)
	}
	rr := d.callReports(t, http.MethodGet, "/", nil)
	var resp struct {
		Reports []*reports.Report `json:"reports"`
		PerUser map[string]int    `json:"reports_per_user"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if resp.PerUser["T1"] != 2 {
		t.Errorf("T1 count: got %d, want 2", resp.PerUser["T1"])
	}
	if resp.PerUser["T2"] != 1 {
		t.Errorf("T2 count: got %d, want 1", resp.PerUser["T2"])
	}
}

func TestReports_List_EmptyReturnsEmptyArray(t *testing.T) {
	t.Parallel()
	d := newTestReportsEnv(t)
	rr := d.callReports(t, http.MethodGet, "/", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d", rr.Code)
	}
	// Decode the raw body and confirm "reports":[], not "null".
	if !strings.Contains(rr.Body.String(), `"reports":[]`) {
		t.Errorf("expected empty array, body=%s", rr.Body.String())
	}
}

func TestReports_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	d := newTestReportsEnv(t)
	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPut, "/"},
		{http.MethodDelete, "/"},
		{http.MethodPatch, "/"},
		{http.MethodPost, "/1"},
		{http.MethodPut, "/1"},
	}
	for _, tc := range cases {
		rr := d.callReports(t, tc.method, tc.path, nil)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: got %d, want 405", tc.method, tc.path, rr.Code)
		}
	}
}

func TestReports_NestedPath404(t *testing.T) {
	t.Parallel()
	d := newTestReportsEnv(t)
	rr := d.callReports(t, http.MethodGet, "/1/extra", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rr.Code)
	}
}
