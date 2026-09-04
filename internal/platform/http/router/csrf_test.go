// --- FILE service.https ---

package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func runCSRF(t *testing.T, method, target, origin, contentType, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := csrfGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSecurityHeadersRejectInlineContent(t *testing.T) {
	h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("Content-Security-Policy header is missing")
	}
	if strings.Contains(csp, "unsafe-inline") {
		t.Fatalf("Content-Security-Policy permits inline content: %q", csp)
	}
}

func TestCSRFGuard(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		target      string
		origin      string
		contentType string
		body        string
		wantCode    int
	}{
		{
			name:     "GET passes without origin",
			method:   http.MethodGet,
			target:   "http://example.com/",
			wantCode: http.StatusOK,
		},
		{
			name:     "POST without origin rejected",
			method:   http.MethodPost,
			target:   "http://example.com/settings",
			wantCode: http.StatusForbidden,
		},
		{
			name:     "POST cross-origin rejected",
			method:   http.MethodPost,
			target:   "http://example.com/settings",
			origin:   "http://evil.example.net",
			wantCode: http.StatusForbidden,
		},
		{
			name:        "POST same-origin JSON passes",
			method:      http.MethodPost,
			target:      "http://example.com/settings",
			origin:      "http://example.com",
			contentType: "application/json",
			body:        `{"logLevel":"info"}`,
			wantCode:    http.StatusOK,
		},
		{
			name:        "POST same-origin HTTPS passes",
			method:      http.MethodPost,
			target:      "https://example.com/settings",
			origin:      "https://example.com",
			contentType: "application/json",
			body:        `{"logLevel":"info"}`,
			wantCode:    http.StatusOK,
		},
		{
			name:        "POST cross-scheme rejected",
			method:      http.MethodPost,
			target:      "https://example.com/settings",
			origin:      "http://example.com",
			contentType: "application/json",
			body:        `{"logLevel":"info"}`,
			wantCode:    http.StatusForbidden,
		},
		{
			name:        "POST same-origin non-JSON body rejected",
			method:      http.MethodPost,
			target:      "http://example.com/settings",
			origin:      "http://example.com",
			contentType: "text/plain",
			body:        "boom",
			wantCode:    http.StatusUnsupportedMediaType,
		},
		{
			name:        "POST login form same-origin passes",
			method:      http.MethodPost,
			target:      "http://example.com/login",
			origin:      "http://example.com",
			contentType: "application/x-www-form-urlencoded",
			body:        "password=x",
			wantCode:    http.StatusOK,
		},
		{
			name:     "POST same-origin without body passes",
			method:   http.MethodPost,
			target:   "http://example.com/logout",
			origin:   "http://example.com",
			wantCode: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := runCSRF(t, tt.method, tt.target, tt.origin, tt.contentType, tt.body)
			if rec.Code != tt.wantCode {
				t.Fatalf("got status %d, want %d", rec.Code, tt.wantCode)
			}
		})
	}
}

func TestCSRFGuardUsesForwardedScheme(t *testing.T) {
	h := csrfGuard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "http://example.com/settings", strings.NewReader(`{}`))
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusOK)
	}
}
