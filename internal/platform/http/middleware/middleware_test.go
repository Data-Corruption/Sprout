// --- FILE service.https ---

package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sprout/internal/app"
	"sprout/internal/build"
	"sprout/internal/platform/database"
	"sprout/internal/platform/database/config"
	"sprout/internal/platform/database/sessions"
	"sprout/internal/types"
	"sprout/pkg/crypto"
	"testing"
	"time"

	"golang.org/x/time/rate"
	"sprout/pkg/xlog"
)

func newTestApp(t *testing.T) *app.App {
	t.Helper()
	tmp := t.TempDir()
	logger, err := xlog.New(filepath.Join(tmp, "logs"), "error")
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	buildInfo := build.BuildInfo{
		Name:               "sprout",
		DefaultLogLevel:    "warn",
		ServiceDefaultPort: 8484,
	}
	db, err := database.New(filepath.Join(tmp, "db"), logger, buildInfo, database.ApplyPendingMigrations)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		logger.Close()
	})
	a := app.New(buildInfo)
	a.DB = db
	a.Log = logger
	return a
}

func addCredential(t *testing.T, a *app.App, username, password string, perms types.Perm) {
	t.Helper()
	hash, salt, err := crypto.HashPassword(password)
	if err != nil {
		t.Fatalf("failed to hash password: %v", err)
	}
	if _, err := config.Update(a.DB, func(cfg *types.Configuration) error {
		cfg.Credentials = append(cfg.Credentials, types.Credential{
			Username: username, PassHash: hash, PassSalt: salt, Perms: perms,
		})
		return nil
	}); err != nil {
		t.Fatalf("failed to add credential: %v", err)
	}
}

func TestSessionCookieName(t *testing.T) {
	if got := SessionCookieName("sprout"); got != "sprout_session" {
		t.Fatalf("cookie name = %q, want sprout_session", got)
	}
	if got := SessionCookieName(""); got != "session" {
		t.Fatalf("fallback cookie name = %q, want session", got)
	}
}

// authRequest runs a request with the given session cookie through Auth and
// returns the recorder plus the perms observed by the next handler.
func authRequest(auth *AuthService, token string) (*httptest.ResponseRecorder, types.Perm) {
	var seenPerms types.Perm
	h := auth.Auth()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPerms = SessionPerms(r)
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if token != "" {
		req.AddCookie(&http.Cookie{Name: SessionCookieName(auth.app.BuildInfo().Name), Value: token})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, seenPerms
}

func TestNewSessionAndAuth(t *testing.T) {
	a := newTestApp(t)
	auth := NewAuthService(a)
	addCredential(t, a, "admin", "correct horse", types.PermAdmin)

	token, msg, err := auth.NewSession("admin", "correct horse")
	if err != nil {
		t.Fatalf("NewSession failed: %v (%s)", err, msg)
	}

	rec, perms := authRequest(auth, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("authenticated request: got status %d, want 200", rec.Code)
	}
	if perms != types.PermAdmin {
		t.Fatalf("session perms = %v, want PermAdmin", perms)
	}
	session, err := sessions.Get(a.DB, crypto.Hash(token))
	if err != nil || session == nil {
		t.Fatalf("read created session: session=%v err=%v", session, err)
	}
	if session.Username != "admin" {
		t.Fatalf("session username = %q, want admin", session.Username)
	}

	// no cookie -> redirect to login
	rec, _ = authRequest(auth, "")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("unauthenticated request: got status %d, want 303", rec.Code)
	}

	// bogus token -> redirect to login
	rec, _ = authRequest(auth, "not-a-real-token")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("bogus token request: got status %d, want 303", rec.Code)
	}
}

func TestNewSessionNormalizesUsername(t *testing.T) {
	a := newTestApp(t)
	auth := NewAuthService(a)
	addCredential(t, a, " Admin ", "right", types.PermAdmin)

	if _, msg, err := auth.NewSession(" ADMIN ", "right"); err != nil {
		t.Fatalf("normalized username rejected: %v (%s)", err, msg)
	}
}

func TestNewSessionUnknownAndWrongUserShareOneComparison(t *testing.T) {
	a := newTestApp(t)
	auth := NewAuthService(a)
	addCredential(t, a, "admin", "right", types.PermAdmin)

	var calls int
	var gotHash, gotSalt string
	auth.comparePassword = func(_, hash, salt string) bool {
		calls++
		gotHash, gotSalt = hash, salt
		return false
	}

	_, msg, err := auth.NewSession("missing", "wrong")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown username error = %v, want ErrInvalidCredentials", err)
	}
	if msg != ErrInvalidCredentials.Error() {
		t.Fatalf("unknown username message = %q", msg)
	}
	if calls != 1 || gotHash != dummyPassHash || gotSalt != dummyPassSalt {
		t.Fatalf("unknown username comparison = calls:%d hash:%q salt:%q", calls, gotHash, gotSalt)
	}

	cfg, err := config.View(a.DB)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	calls = 0
	_, msg, err = auth.NewSession("admin", "wrong")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password error = %v, want ErrInvalidCredentials", err)
	}
	if msg != ErrInvalidCredentials.Error() {
		t.Fatalf("wrong password message = %q", msg)
	}
	if calls != 1 || gotHash != cfg.Credentials[0].PassHash || gotSalt != cfg.Credentials[0].PassSalt {
		t.Fatalf("known username comparison = calls:%d hash:%q salt:%q", calls, gotHash, gotSalt)
	}
}

func TestNewSessionBoundsPasswordVerification(t *testing.T) {
	a := newTestApp(t)
	auth := NewAuthService(a)
	addCredential(t, a, "admin", "right", types.PermAdmin)

	auth.passwordSlots = make(chan struct{}, 1)
	auth.passwordSlots <- struct{}{}
	called := false
	auth.comparePassword = func(_, _, _ string) bool {
		called = true
		return true
	}

	if _, _, err := auth.NewSession("admin", "right"); !errors.Is(err, ErrTooManyRequests) {
		t.Fatalf("full verification bound error = %v, want ErrTooManyRequests", err)
	}
	if called {
		t.Fatal("password comparison ran without an available slot")
	}
}

func TestNewSessionFailedLoginRateLimit(t *testing.T) {
	a := newTestApp(t)
	auth := NewAuthService(a)
	addCredential(t, a, "admin", "right", types.PermAdmin)

	// burst of 3 attempts pass the limiter and report generic invalid credentials
	for i := 0; i < 3; i++ {
		_, msg, err := auth.NewSession("admin", "wrong")
		if err == nil {
			t.Fatalf("attempt %d: expected error for wrong password", i+1)
		}
		if errors.Is(err, ErrTooManyRequests) {
			t.Fatalf("attempt %d: rate limited too early", i+1)
		}
		if msg != "invalid username or password" {
			t.Fatalf("attempt %d: msg = %q, want %q", i+1, msg, "invalid username or password")
		}
	}

	// 4th failed attempt exhausts the burst -> non-blocking 429
	start := time.Now()
	_, msg, err := auth.NewSession("admin", "wrong")
	if !errors.Is(err, ErrTooManyRequests) {
		t.Fatalf("expected ErrTooManyRequests, got %v", err)
	}
	if msg != "too many requests" {
		t.Fatalf("msg = %q, want %q", msg, "too many requests")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("rate-limited attempt blocked for %v, want immediate return", elapsed)
	}

	// The limiter runs before password verification, so it also sheds a valid
	// attempt while exhausted rather than doing unbounded Argon2 work.
	if _, _, err := auth.NewSession("admin", "right"); !errors.Is(err, ErrTooManyRequests) {
		t.Fatalf("correct password should be rate limited while exhausted, got %v", err)
	}
}

func TestAuthExpiredSession(t *testing.T) {
	a := newTestApp(t)
	auth := NewAuthService(a)

	token := "expired-token"
	if err := sessions.Create(a.DB, crypto.Hash(token), sessions.Session{
		Expiry: time.Now().Add(-time.Minute), Perms: types.PermAdmin, Username: "admin",
	}); err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	rec, _ := authRequest(auth, token)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expired session: got status %d, want 303", rec.Code)
	}
}

// TestAuthDoesNotExtendExpiry pins the fixed-lifetime contract: the cookie is
// issued once with MaxAge = SessionDuration, so the stored expiry must not
// move or the browser and database would disagree about when a login ends.
func TestAuthDoesNotExtendExpiry(t *testing.T) {
	a := newTestApp(t)
	auth := NewAuthService(a)

	token := "fixed-token"
	hash := crypto.Hash(token)
	expiry := time.Now().Add(time.Minute).Truncate(time.Second)
	if err := sessions.Create(a.DB, hash, sessions.Session{
		Expiry: expiry, Perms: types.PermSettings, Username: "admin",
	}); err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	rec, _ := authRequest(auth, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid session: got status %d, want 200", rec.Code)
	}

	s, err := sessions.Get(a.DB, hash)
	if err != nil || s == nil {
		t.Fatalf("failed to read session back: %v", err)
	}
	if !s.Expiry.Equal(expiry) {
		t.Fatalf("expiry changed after an authenticated request: got %v, want %v", s.Expiry, expiry)
	}
}

func TestCredentialRemovalRevokesSessions(t *testing.T) {
	a := newTestApp(t)
	auth := NewAuthService(a)
	addCredential(t, a, "victim", "pw1", types.PermAdmin)
	addCredential(t, a, "keeper", "pw2", types.PermAdmin)

	victimToken, msg, err := auth.NewSession("victim", "pw1")
	if err != nil {
		t.Fatalf("NewSession(victim) failed: %v (%s)", err, msg)
	}
	keeperToken, msg, err := auth.NewSession("keeper", "pw2")
	if err != nil {
		t.Fatalf("NewSession(keeper) failed: %v (%s)", err, msg)
	}

	// revoke all of victim's sessions (what `users remove` does)
	n, err := sessions.DeleteByUsername(a.DB, "victim")
	if err != nil {
		t.Fatalf("DeleteByUsername failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("DeleteByUsername removed %d sessions, want 1", n)
	}

	if rec, _ := authRequest(auth, victimToken); rec.Code != http.StatusSeeOther {
		t.Fatalf("revoked session: got status %d, want 303", rec.Code)
	}
	if rec, _ := authRequest(auth, keeperToken); rec.Code != http.StatusOK {
		t.Fatalf("unrelated session: got status %d, want 200", rec.Code)
	}
}

func TestDeleteSession(t *testing.T) {
	a := newTestApp(t)
	auth := NewAuthService(a)
	addCredential(t, a, "admin", "pw", types.PermAdmin)

	token, msg, err := auth.NewSession("admin", "pw")
	if err != nil {
		t.Fatalf("NewSession failed: %v (%s)", err, msg)
	}

	if err := auth.DeleteSession(token); err != nil {
		t.Fatalf("DeleteSession failed: %v", err)
	}
	if rec, _ := authRequest(auth, token); rec.Code != http.StatusSeeOther {
		t.Fatalf("deleted session request: got status %d, want 303", rec.Code)
	}
	// deleting again is a no-op, not an error
	if err := auth.DeleteSession(token); err != nil {
		t.Fatalf("second DeleteSession failed: %v", err)
	}
}

func TestSessionsSurviveMiddlewareRebuild(t *testing.T) {
	// simulates a service restart: sessions live in the DB, so a freshly
	// constructed Auth middleware over the same DB accepts existing sessions
	a := newTestApp(t)
	auth := NewAuthService(a)
	addCredential(t, a, "admin", "pw", types.PermAdmin)

	token, msg, err := auth.NewSession("admin", "pw")
	if err != nil {
		t.Fatalf("NewSession failed: %v (%s)", err, msg)
	}

	// Rebuild the router-owned service twice to make restart behavior explicit.
	for i := 0; i < 2; i++ {
		if rec, _ := authRequest(NewAuthService(a), token); rec.Code != http.StatusOK {
			t.Fatalf("rebuild %d: got status %d, want 200", i+1, rec.Code)
		}
	}
}

func TestAuthServiceInstancesHaveIndependentLoginLimits(t *testing.T) {
	a := newTestApp(t)
	addCredential(t, a, "admin", "right", types.PermAdmin)
	exhausted := NewAuthService(a)
	fresh := NewAuthService(a)

	exhausted.loginLimiter = rate.NewLimiter(rate.Limit(0), 0)
	if _, _, err := exhausted.NewSession("admin", "right"); !errors.Is(err, ErrTooManyRequests) {
		t.Fatalf("exhausted service error = %v, want ErrTooManyRequests", err)
	}
	if _, _, err := fresh.NewSession("admin", "right"); err != nil {
		t.Fatalf("fresh service inherited another service's limit: %v", err)
	}
}

func TestAuthServiceInstancesHaveIndependentComparisonHooks(t *testing.T) {
	a := newTestApp(t)
	addCredential(t, a, "admin", "right", types.PermAdmin)
	custom := NewAuthService(a)
	fresh := NewAuthService(a)
	custom.comparePassword = func(_, _, _ string) bool { return false }

	if _, _, err := custom.NewSession("admin", "right"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("custom comparison error = %v, want ErrInvalidCredentials", err)
	}
	if _, _, err := fresh.NewSession("admin", "right"); err != nil {
		t.Fatalf("fresh service inherited another service's comparison hook: %v", err)
	}
}
