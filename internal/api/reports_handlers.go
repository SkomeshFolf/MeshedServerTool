package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/Skomesh/MeshedServerTool/internal/hub"
	"github.com/Skomesh/MeshedServerTool/internal/reports"
	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

// v1ReportsDeps wires the reports API.
type v1ReportsDeps struct {
	store *reports.Store
	hub   *hub.Hub
}

// ServeHTTP routes /api/v1/reports and /api/v1/reports/{id}.
//
//	GET    /api/v1/reports?handled=true&server=foo&limit=100
//	POST   /api/v1/reports   {body}
//	GET    /api/v1/reports/{id}
//	PATCH  /api/v1/reports/{id}  {handled: true|false}
//	DELETE /api/v1/reports/{id}
func (d *v1ReportsDeps) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	parts := strings.SplitN(path, "/", 2)
	idStr := parts[0]
	rest := ""
	if len(parts) > 1 {
		rest = parts[1]
	}

	if idStr == "" {
		switch r.Method {
		case http.MethodGet:
			d.list(w, r)
		case http.MethodPost:
			d.create(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	if rest != "" {
		http.Error(w, "not found: "+r.URL.Path, http.StatusNotFound)
		return
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		d.get(w, r, id)
	case http.MethodPatch:
		d.update(w, r, id)
	case http.MethodDelete:
		d.delete(w, r, id)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (d *v1ReportsDeps) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var handled *bool
	if v := q.Get("handled"); v != "" {
		b := v == "true" || v == "1"
		handled = &b
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit == 0 {
		limit = 200
	}
	reps, err := d.store.List(r.Context(), handled, q.Get("server"), limit)
	if err != nil {
		log.Printf("list reports: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	perUser, err := d.store.CountByTarget(r.Context())
	if err != nil {
		log.Printf("count reports: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"reports":          reportsOrEmpty(reps),
		"reports_per_user": perUser,
	})
}

// reportsOrEmpty returns the slice or an empty slice so the JSON
// response is `[]` rather than `null` when there are no reports.
// Clients (including the React UI) call .length on the array —
// null breaks that.
func reportsOrEmpty(reps []*reports.Report) []*reports.Report {
	if reps == nil {
		return []*reports.Report{}
	}
	return reps
}

func (d *v1ReportsDeps) create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ServerName string `json:"server_name"`
		TargetID   string `json:"target_id"`
		TargetName string `json:"target_name"`
		SourceID   string `json:"source_id"`
		SourceName string `json:"source_name"`
		Date       string `json:"date"`
		Reason     string `json:"reason"`
		Text       string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.ServerName == "" || body.TargetID == "" || body.SourceID == "" {
		writeJSONError(w, http.StatusBadRequest, "server_name, target_id, source_id are required")
		return
	}
	rep := &reports.Report{
		ServerName: body.ServerName,
		TargetID:   body.TargetID,
		TargetName: body.TargetName,
		SourceID:   body.SourceID,
		SourceName: body.SourceName,
		Date:       body.Date,
		Reason:     body.Reason,
		Text:       body.Text,
	}
	created, exists, err := d.store.CreateReport(r.Context(), rep)
	if err != nil {
		log.Printf("create report: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	d.hub.Publish(hub.Event{Type: "report.new", Data: created})
	status := http.StatusCreated
	if exists {
		status = http.StatusOK // de-dup: 200 instead of 201
	}
	writeJSON(w, status, created)
}

func (d *v1ReportsDeps) get(w http.ResponseWriter, r *http.Request, id int64) {
	rep, err := d.store.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (d *v1ReportsDeps) update(w http.ResponseWriter, r *http.Request, id int64) {
	var body struct {
		Handled *bool `json:"handled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Handled == nil {
		writeJSONError(w, http.StatusBadRequest, "handled field is required")
		return
	}
	if err := d.store.MarkHandled(r.Context(), id, *body.Handled); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	rep, _ := d.store.GetByID(r.Context(), id)
	d.hub.Publish(hub.Event{Type: "report.updated", Data: rep})
	writeJSON(w, http.StatusOK, rep)
}

func (d *v1ReportsDeps) delete(w http.ResponseWriter, r *http.Request, id int64) {
	if err := d.store.Delete(r.Context(), id); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	d.hub.Publish(hub.Event{Type: "report.deleted", Data: id})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
