// --- FILE service.https ---

// Package middleware implements session-based HTTP auth for the dashboard:
// sessions stored in the SQLite sessions table keyed by SHA256 of the cookie
// token, a fixed 30-minute lifetime from login, Argon2id-verified credentials
// from the SQLite config, and bounded login attempts. Sized for a few users on
// a dashboard-style UI (see the sessions package if you're outgrowing it).
//
// Sessions deliberately do not slide. The cookie and the database row expire
// together at SessionDuration after login, so a stolen or forgotten session
// has a hard upper bound. Applications that need long-lived logins should add
// an explicit refresh design rather than extending this one.
package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sprout/internal/app"
	"sprout/internal/platform/database/config"
	"sprout/internal/platform/database/sessions"
	"sprout/internal/platform/http/cookies"
	"sprout/internal/types"
	"sprout/pkg/crypto"
	"time"

	"sprout/pkg/xhttp"

	"golang.org/x/time/rate"
)

// SessionCookieName derives the cookie name from injected build information.
// It falls back to a bare "session" when the app name is unset.
func SessionCookieName(appName string) string {
	if appName != "" {
		return appName + "_session"
	}
	return "session"
}

// SessionDuration bounds both the cookie MaxAge and the stored session row.
const SessionDuration = 30 * time.Minute

// ErrTooManyRequests is returned by NewSession when the login limiter or
// password-verification concurrency bound is exhausted.
var ErrTooManyRequests = fmt.Errorf("too many requests")

var ErrInvalidCredentials = errors.New("invalid username or password")

// sessionAuth is the per-request authorization payload stored in the context.
type sessionAuth struct {
	Perms types.Perm
}

type authKeyType struct{}

var authKey authKeyType

// AuthService owns the authentication state for one dashboard router. Login
// attempts share one small limiter and one password-verification bound within
// that router, without coupling unrelated App instances in the same process.
type AuthService struct {
	app             *app.App
	loginLimiter    *rate.Limiter
	passwordSlots   chan struct{}
	comparePassword func(password, hash, salt string) bool
}

// NewAuthService constructs the authentication service for one router.
func NewAuthService(a *app.App) *AuthService {
	return &AuthService{
		app:             a,
		loginLimiter:    rate.NewLimiter(rate.Limit(0.25), 3),
		passwordSlots:   make(chan struct{}, 2),
		comparePassword: crypto.ComparePasswords,
	}
}

const (
	dummyPassHash = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	dummyPassSalt = "sprout-dummy-login-salt"
)

// NewSession validates one normalized username/password pair and mints a
// session token. Unknown usernames perform the same single Argon2 comparison
// as wrong passwords.
func (s *AuthService) NewSession(username, password string) (string, string, error) {
	if !s.loginLimiter.Allow() {
		return "", "too many requests", ErrTooManyRequests
	}

	cfg, err := config.View(s.app.DB)
	if err != nil {
		return "", "internal error", err
	}

	username = types.NormalizeUsername(username)
	matched := types.Credential{
		PassHash: dummyPassHash,
		PassSalt: dummyPassSalt,
	}
	found := false
	for i := range cfg.Credentials {
		if cfg.Credentials[i].Username == username {
			matched = cfg.Credentials[i]
			found = true
			break
		}
	}

	select {
	case s.passwordSlots <- struct{}{}:
		defer func() { <-s.passwordSlots }()
	default:
		return "", "too many requests", ErrTooManyRequests
	}
	passwordMatches := s.comparePassword(password, matched.PassHash, matched.PassSalt)
	if !found || !passwordMatches {
		return "", ErrInvalidCredentials.Error(), ErrInvalidCredentials
	}

	token, err := crypto.GenRandomString(32)
	if err != nil {
		return "", "internal error", err
	}

	// opportunistic housekeeping; a failure here shouldn't block the login
	if err := sessions.DeleteExpired(s.app.DB); err != nil {
		s.app.Log.Warnf("failed to prune expired sessions: %v", err)
	}

	if err := sessions.Create(s.app.DB, crypto.Hash(token), sessions.Session{
		Expiry:   time.Now().Add(SessionDuration),
		Perms:    matched.Perms,
		Username: matched.Username,
	}); err != nil {
		return "", "internal error", err
	}

	return token, "", nil
}

// DeleteSession removes the session minted for the given cookie token (e.g.
// on logout). Deleting an unknown token is not an error.
func (s *AuthService) DeleteSession(token string) error {
	_, err := sessions.Delete(s.app.DB, crypto.Hash(token))
	return err
}

// SessionPerms returns the permissions associated with the current request's session.
func SessionPerms(r *http.Request) types.Perm {
	if s, ok := r.Context().Value(authKey).(sessionAuth); ok {
		return s.Perms
	}
	return 0
}

// RequirePerm returns an HTTP 403 error if the request session lacks the given permission.
func RequirePerm(r *http.Request, p types.Perm) error {
	if !SessionPerms(r).Has(p) {
		return &xhttp.Err{Code: http.StatusForbidden, Msg: "insufficient permissions"}
	}
	return nil
}

// DevAuth returns middleware that grants PermAdmin to every request,
// bypassing session cookies and rate limiting. Only used in dev-mode builds
// (the default local build.sh mode).
func DevAuth() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), authKey, sessionAuth{Perms: types.PermAdmin})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Auth returns middleware for routes that require a valid session. Sessions
// live in the DB, so they survive restarts and CLI-side revocation (credential
// removal) takes effect immediately.
func (s *AuthService) Auth() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := cookies.Read(r, SessionCookieName(s.app.BuildInfo().Name))
			if token == "" {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			session, err := sessions.Get(s.app.DB, crypto.Hash(token))
			if err != nil {
				s.app.Log.Errorf("session lookup failed: %v", err)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}
			if session == nil { // no session, or expired
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			ctx := context.WithValue(r.Context(), authKey, sessionAuth{Perms: session.Perms})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
