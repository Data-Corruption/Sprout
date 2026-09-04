// --- FILE service.https ---

package router

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"sprout/internal/app"
	"sprout/internal/build"
	"sprout/internal/platform/database"
	"sprout/internal/ui"
	"sprout/pkg/xlog"
)

func newRouterTestApp(t *testing.T) *app.App {
	t.Helper()
	tmp := t.TempDir()
	logger, err := xlog.New(filepath.Join(tmp, "logs"), "error")
	if err != nil {
		t.Fatal(err)
	}
	info := build.BuildInfo{
		Name:               "sprout",
		Version:            "v1.0.0",
		DefaultLogLevel:    "warn",
		ServiceDefaultPort: 8484,
	}
	db, err := database.New(filepath.Join(tmp, "db"), logger, info, database.ApplyPendingMigrations)
	if err != nil {
		logger.Close()
		t.Fatal(err)
	}
	dashboard, err := ui.New()
	if err != nil {
		db.Close()
		logger.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		logger.Close()
	})
	a := app.New(info)
	a.DB = db
	a.Log = logger
	a.UI = dashboard
	return a
}

func TestRouterKeepsLoginAndAssetsPublic(t *testing.T) {
	r := New(newRouterTestApp(t))

	for _, target := range []string{"/login", "/assets/not-found"} {
		req := httptest.NewRequest(http.MethodGet, "https://example.com"+target, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code == http.StatusSeeOther && rec.Header().Get("Location") == "/login" {
			t.Fatalf("%s was redirected through auth", target)
		}
		if rec.Header().Get("Content-Security-Policy") == "" {
			t.Fatalf("%s response is missing security headers", target)
		}
	}
}

func TestRouterHealthIsPublicAndMinimal(t *testing.T) {
	r := New(newRouterTestApp(t))
	req := httptest.NewRequest(http.MethodGet, "https://example.com/healthz", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("health response = %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); body != "ok\n" {
		t.Fatalf("health body = %q, want %q", body, "ok\n")
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("health Content-Type = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("health Cache-Control = %q", got)
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("health response is missing security headers")
	}
}

func TestRouterLoginLimitDoesNotThrottleAssets(t *testing.T) {
	r := New(newRouterTestApp(t))
	for range 4 {
		req := httptest.NewRequest(
			http.MethodPost,
			"https://example.com/login",
			strings.NewReader("username=missing&password=wrong"),
		)
		req.Header.Set("Origin", "https://example.com")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
	}

	req := httptest.NewRequest(http.MethodGet, "https://example.com/assets/not-found", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code == http.StatusTooManyRequests {
		t.Fatal("asset request was throttled by exhausted login limiter")
	}
	if rec.Code == http.StatusSeeOther && rec.Header().Get("Location") == "/login" {
		t.Fatal("asset request was redirected through auth")
	}
}

func TestRouterProtectsSettings(t *testing.T) {
	r := New(newRouterTestApp(t))
	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("unauthenticated settings response = %d location %q, want 303 to /login",
			rec.Code, rec.Header().Get("Location"))
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("auth redirect is missing security headers")
	}
}

func TestRouterAppliesCSRFToPublicLoginAndProtectedSettings(t *testing.T) {
	r := New(newRouterTestApp(t))
	tests := []struct {
		target string
		body   string
	}{
		{target: "/login", body: "username=admin&password=wrong"},
		{target: "/settings", body: `{"logLevel":"info"}`},
	}
	for _, tt := range tests {
		req := httptest.NewRequest(http.MethodPost, "https://example.com"+tt.target, strings.NewReader(tt.body))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("POST %s without Origin = %d, want 403", tt.target, rec.Code)
		}
	}
}
