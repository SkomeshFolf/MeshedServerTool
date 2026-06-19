// Package auth implements password hashing, session token generation, and
// an HTTP middleware that gates /api/v1/* routes behind a valid session cookie.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

// cookieSecureMu guards the package-level cookieSecureOverride and
// cookieSecureHook vars. They're process-wide configuration set at
// startup (or in tests), so we serialize reads/writes even though
// the production access pattern is single-writer / many-readers.
var cookieSecureMu sync.RWMutex

// SessionCookieName is the HTTP cookie carrying the session token.
const SessionCookieName = "meshed_session"

// SessionDuration is how long a session token is valid from creation.
// Renewed on activity via TouchSession? No — for now we keep a hard expiry
// to bound the window of a stolen token. Users re-login after this.
const SessionDuration = 7 * 24 * time.Hour

// ErrInvalidCredentials is returned when login fails (no such user or wrong password).
var ErrInvalidCredentials = errors.New("invalid credentials")

// ErrNoUsers is returned by CreateFirstUser if any user already exists.
var ErrNoUsers = errors.New("users already exist; first-user bootstrap is disabled")

// HashPassword returns a bcrypt hash of the password at the default cost.
func HashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// CheckPassword reports whether the password matches the bcrypt hash.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// NewSessionToken returns a 32-byte cryptographically random token, hex-encoded.
func NewSessionToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// Service bundles the dependencies for auth operations.
type Service struct {
	store *storage.Store
}

func NewService(store *storage.Store) *Service {
	return &Service{store: store}
}

// Login authenticates a user by username+password, creates a new session, and
// returns the session token. On failure returns ErrInvalidCredentials.
func (s *Service) Login(ctx context.Context, username, password, userAgent, ip string) (string, *storage.User, error) {
	user, err := s.store.Users().GetUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return "", nil, ErrInvalidCredentials
		}
		return "", nil, err
	}
	if !CheckPassword(user.PasswordHash, password) {
		return "", nil, ErrInvalidCredentials
	}

	token, err := NewSessionToken()
	if err != nil {
		return "", nil, err
	}
	now := time.Now().UTC()
	sess := &storage.Session{
		Token:      token,
		UserID:     user.ID,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(SessionDuration),
		UserAgent:  userAgent,
		IP:         ip,
	}
	if err := s.store.Sessions().CreateSession(ctx, sess); err != nil {
		return "", nil, err
	}
	if err := s.store.Users().MarkLoggedIn(ctx, user.ID); err != nil {
		// Non-fatal; log in caller. We still return the session.
		_ = err
	}
	return token, user, nil
}

// Logout deletes the session with the given token. Idempotent.
func (s *Service) Logout(ctx context.Context, token string) error {
	return s.store.Sessions().DeleteSession(ctx, token)
}

// Resolve returns the user behind a session token, or storage.ErrNotFound.
// If the session has expired, it's deleted and ErrNotFound is returned.
func (s *Service) Resolve(ctx context.Context, token string) (*storage.User, error) {
	sess, err := s.store.Sessions().GetSession(ctx, token)
	if err != nil {
		return nil, err
	}
	if time.Now().UTC().After(sess.ExpiresAt) {
		_ = s.store.Sessions().DeleteSession(ctx, token)
		return nil, storage.ErrNotFound
	}
	// Touch is best-effort; don't fail the request if it errors.
	_ = s.store.Sessions().TouchSession(ctx, token)
	return s.store.Users().GetUserByID(ctx, sess.UserID)
}

// CreateFirstUser creates the very first user, gated on the users table being
// empty. Returns ErrNoUsers if any user already exists.
//
// Uses CreateFirstUserTx so two concurrent bootstrap calls can't both
// succeed: with SetMaxOpenConns(1) the second BeginTx blocks until the
// first commits, then the inner SELECT COUNT sees the inserted row.
func (s *Service) CreateFirstUser(ctx context.Context, username, password string) (*storage.User, error) {
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	user, err := s.store.Users().CreateFirstUserTx(ctx, username, hash, storage.RoleAdmin)
	if err != nil {
		// Translate storage.ErrNoUsers to auth.ErrNoUsers so the API
		// handler can map it to a 403. The handler checks for
		// auth.ErrNoUsers specifically.
		if errors.Is(err, storage.ErrNoUsers) {
			return nil, ErrNoUsers
		}
		return nil, err
	}
	return user, nil
}

// SetSessionCookie writes the session token as a cookie on the response.
// Secure flag is set when the request was received over HTTPS OR when
// an X-Forwarded-Proto: https header is present from a trusted proxy.
// The override pointer (set via SetCookieSecureOverride) lets operators
// force Secure on (recommended for production) or off (only for HTTP
// local testing). (HIGH-1)
func SetSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   shouldUseSecureCookie(r),
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(SessionDuration),
		MaxAge:   int(SessionDuration.Seconds()),
	})
}

// ClearSessionCookie invalidates the cookie client-side. Secure flag
// follows the same rule as SetSessionCookie.
func ClearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   shouldUseSecureCookie(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// cookieSecureOverride is a package-level override for the Secure flag.
// nil (default) = derive from r.TLS / X-Forwarded-Proto.
// &true = always Secure (recommended in production).
// &false = never Secure (HTTP local testing only).
//
// Set via SetCookieSecureOverride from main.go at startup.
var cookieSecureOverride *bool

// SetCookieSecureOverride configures the global cookie Secure behavior.
// Pass nil to revert to the r.TLS-derived default.
func SetCookieSecureOverride(v *bool) {
	cookieSecureMu.Lock()
	cookieSecureOverride = v
	cookieSecureMu.Unlock()
}

// shouldUseSecureCookie decides whether to mark the session cookie
// Secure on this request. The override wins; otherwise we defer to the
// optional hook (set by the api package via WithCookieSecureHook), and
// if that's not configured we fall back to r.TLS != nil.
func shouldUseSecureCookie(r *http.Request) bool {
	cookieSecureMu.RLock()
	override := cookieSecureOverride
	hook := cookieSecureHook
	cookieSecureMu.RUnlock()
	if override != nil {
		return *override
	}
	if hook != nil {
		return hook(r)
	}
	return r.TLS != nil
}

// cookieSecureHook is an optional function set by the api package
// (via WithCookieSecureHook) that derives the Secure flag from the
// full request — typically to honor X-Forwarded-Proto from trusted
// proxies. When nil, the default r.TLS check is used.
var cookieSecureHook func(r *http.Request) bool

// WithCookieSecureHook registers a function that decides the Secure
// flag for a given request. Called by the api package after parsing
// --trusted-proxies. Returns the previous hook so callers can chain.
func WithCookieSecureHook(fn func(r *http.Request) bool) func(r *http.Request) bool {
	cookieSecureMu.Lock()
	prev := cookieSecureHook
	cookieSecureHook = fn
	cookieSecureMu.Unlock()
	return prev
}

// TokenFromRequest extracts the session token from the request, or "" if absent.
func TokenFromRequest(r *http.Request) string {
	c, err := r.Cookie(SessionCookieName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(c.Value)
}

// Middleware returns an http.Handler that runs the next handler only if the
// request carries a valid session. The resolved user is attached to the
// request context under userCtxKey.
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := TokenFromRequest(r)
		if token == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		user, err := s.Resolve(r.Context(), token)
		if err != nil {
			ClearSessionCookie(w, r)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), userCtxKey{}, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// userCtxKey is a private type to avoid collisions in the request context.
type userCtxKey struct{}

// UserFromContext returns the authenticated user attached by Middleware, or nil.
func UserFromContext(ctx context.Context) *storage.User {
	u, _ := ctx.Value(userCtxKey{}).(*storage.User)
	return u
}
