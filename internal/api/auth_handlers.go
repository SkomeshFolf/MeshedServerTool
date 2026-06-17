package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/Skomesh/MeshedServerTool/internal/auth"
	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

// v1AuthDeps wires the auth endpoints. Auth routes are public (login,
// bootstrap) plus the authenticated /me route, all under /api/v1/auth.
type v1AuthDeps struct {
	svc *auth.Service
	store *storage.Store
}

// handleLogin authenticates a user. POST /api/v1/auth/login
//
// Body: {"username": "...", "password": "..."}
//
// On success, sets the session cookie and returns 200 with
// {"username": "...", "role": "admin|user"}.
func (d *v1AuthDeps) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Username == "" || body.Password == "" {
		writeJSONError(w, http.StatusBadRequest, "username and password are required")
		return
	}

	token, user, err := d.svc.Login(r.Context(), body.Username, body.Password, r.UserAgent(), clientIP(r))
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			writeJSONError(w, http.StatusUnauthorized, "invalid username or password")
			return
		}
		log.Printf("login error: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	auth.SetSessionCookie(w, r, token)
	writeJSON(w, http.StatusOK, map[string]any{
		"username": user.Username,
		"role":     user.Role,
	})
}

// handleLogout destroys the current session. POST /api/v1/auth/logout
func (d *v1AuthDeps) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if token := auth.TokenFromRequest(r); token != "" {
		if err := d.svc.Logout(r.Context(), token); err != nil {
			log.Printf("logout error: %v", err)
		}
	}
	auth.ClearSessionCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleMe returns the current user. GET /api/v1/auth/me (authenticated)
func (d *v1AuthDeps) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"username": user.Username,
		"role":     user.Role,
	})
}

// handleBootstrap creates the first user. POST /api/v1/auth/bootstrap
//
// Disabled once any user exists, so this can't be used to take over an
// existing install.
func (d *v1AuthDeps) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validateUsername(body.Username); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validatePassword(body.Password); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	user, err := d.svc.CreateFirstUser(r.Context(), body.Username, body.Password)
	if err != nil {
		if errors.Is(err, auth.ErrNoUsers) {
			writeJSONError(w, http.StatusForbidden, "users already exist; bootstrap is disabled")
			return
		}
		log.Printf("bootstrap error: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Auto-login the new user.
	token, err := auth.NewSessionToken()
	if err != nil {
		log.Printf("token error: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	now := nowUTC()
	sess := &storage.Session{
		Token:      token,
		UserID:     user.ID,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(auth.SessionDuration),
		UserAgent:  r.UserAgent(),
		IP:         clientIP(r),
	}
	if err := d.store.Sessions().CreateSession(r.Context(), sess); err != nil {
		log.Printf("session create error: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	auth.SetSessionCookie(w, r, token)
	writeJSON(w, http.StatusCreated, map[string]any{
		"username": user.Username,
		"role":     user.Role,
	})
}

// handleStatus reports whether bootstrap is still available and whether
// the caller is authenticated. GET /api/v1/auth/status
//
// Public route — we look up the user from the cookie ourselves rather
// than relying on the middleware so this endpoint can report partial
// state (e.g. an expired session is reported as "not authenticated"
// instead of 401'ing).
func (d *v1AuthDeps) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	count, err := d.store.Users().CountUsers(r.Context())
	if err != nil {
		log.Printf("status error: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	resp := map[string]any{
		"bootstrap_available": count == 0,
		"authenticated":       false,
	}
	if token := auth.TokenFromRequest(r); token != "" {
		if user, err := d.svc.Resolve(r.Context(), token); err == nil && user != nil {
			resp["authenticated"] = true
			resp["username"] = user.Username
			resp["role"] = user.Role
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// validateUsername applies a reasonable policy: 3-32 chars, [a-zA-Z0-9_-].
// This is the only validation the v2 README implies; we keep it strict.
func validateUsername(u string) error {
	if len(u) < 3 || len(u) > 32 {
		return errors.New("username must be 3-32 characters")
	}
	for _, r := range u {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') &&
			!(r >= '0' && r <= '9') && r != '_' && r != '-' {
			return errors.New("username may only contain letters, digits, _ and -")
		}
	}
	return nil
}

// validatePassword applies a minimum-length policy. v2 didn't enforce
// password rules; we add a soft minimum to avoid the empty-password footgun.
func validatePassword(p string) error {
	if len(p) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	if len(p) > 256 {
		return errors.New("password is too long")
	}
	return nil
}

// clientIP returns the best-guess client IP, honoring X-Forwarded-For if
// present. Sufficient for session audit logs at this stage.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first hop (the original client).
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	// r.RemoteAddr is "host:port" — strip the port.
	addr := r.RemoteAddr
	if i := strings.LastIndexByte(addr, ':'); i > 0 {
		return addr[:i]
	}
	return addr
}
