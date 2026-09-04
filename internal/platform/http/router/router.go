// --- FILE service.https ---

package router

import (
	"net"
	"net/http"
	"net/url"
	"sprout/internal/app"
	"sprout/internal/platform/http/middleware"
	"sprout/internal/platform/http/router/settings"
	"strings"

	"github.com/go-chi/chi/v5"
	"sprout/pkg/xlog"
)

func New(a *app.App) *chi.Mux {
	r := chi.NewRouter()

	// inject logger into request context so we can use xhttp.Error() handler
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(xlog.IntoContext(r.Context(), a.Log)))
		})
	})

	// basic security hardening
	if !a.DevMode && strings.HasPrefix(a.BaseURL, "https://") {
		r.Use(httpsRedirect)
	}
	r.Use(securityHeaders)

	// CSRF applies to public login submissions as well as authenticated state
	// changes. Development builds are local-only and bypass dashboard auth.
	if !a.DevMode {
		r.Use(csrfGuard)
	}

	auth := middleware.NewAuthService(a)

	// Static assets and login are public. Security headers and production CSRF
	// checks still wrap them because those middleware are mounted above.
	r.Get("/healthz", handleHealth)
	r.Get("/assets/*", a.UI.ServeAsset)
	RegisterLoginRoutes(a, auth, r)

	// Everything else requires a session. Dev-mode builds grant the protected
	// routes admin permissions without creating a session.
	r.Group(func(protected chi.Router) {
		if a.DevMode {
			protected.Use(middleware.DevAuth())
		} else {
			protected.Use(auth.Auth())
		}
		protected.Post("/logout", handleLogout(a, auth))
		settings.Register(a, protected)
	})

	return r
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

// csrfGuard rejects cross-origin state-changing requests. For unsafe methods it
// requires a same-origin Origin header, and for JSON APIs with a body it
// requires an application/json content type. Safe methods pass untouched.
func csrfGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if origin == "" {
			http.Error(w, "missing Origin header", http.StatusForbidden)
			return
		}
		if u, err := url.Parse(origin); err != nil || u.Scheme != requestScheme(r) || u.Host != r.Host {
			http.Error(w, "cross-origin request rejected", http.StatusForbidden)
			return
		}
		// JSON endpoints must receive JSON; /login is the only form-encoded POST
		if r.URL.Path != "/login" && r.ContentLength != 0 {
			if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				http.Error(w, "expected application/json", http.StatusUnsupportedMediaType)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// requestScheme reports the scheme the client used. X-Forwarded-Proto is
// trusted without a proxy allowlist, which is sound only because the sole
// plain-HTTP listener is the loopback-validated ProxyBind that exists to sit
// behind a local TLS-terminating proxy (see server.New); every other request
// arrives with r.TLS set and the header is ignored. Adding a non-loopback
// plain listener would require per-listener trust here and in httpsRedirect.
func requestScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		return proto
	}
	return "http"
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; frame-ancestors 'self'")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		next.ServeHTTP(w, r)
	})
}

// httpsRedirect sends non-loopback plain-HTTP requests to HTTPS. It trusts
// X-Forwarded-Proto under the same loopback-proxy invariant as requestScheme.
func httpsRedirect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Forwarded-Proto") == "http" || (r.TLS == nil && r.Header.Get("X-Forwarded-Proto") == "") {
			if !isLoopbackHost(r.Host) && r.Host != "" {
				target := "https://" + r.Host + r.URL.RequestURI()
				http.Redirect(w, r, target, http.StatusSeeOther)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopbackHost(hostport string) bool {
	host := hostport
	if parsedHost, _, err := net.SplitHostPort(hostport); err == nil {
		host = parsedHost
	}
	host = strings.Trim(host, "[]")
	return strings.EqualFold(host, "localhost") || net.ParseIP(host).IsLoopback()
}
