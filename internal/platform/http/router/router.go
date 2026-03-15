package router

import (
	"net/http"
	"sprout/internal/app"
	"sprout/internal/platform/http/router/settings"
	"strings"

	"github.com/Data-Corruption/stdx/xlog"
	"github.com/go-chi/chi/v5"
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
	if a.BuildInfo().Version != "vX.X.X" && strings.HasPrefix(a.BaseURL, "https://") {
		r.Use(httpsRedirect)
	}
	r.Use(securityHeaders)

	// serve embedded assets with cache busting
	r.Get("/assets/*", a.UI.ServeAsset)

	// serve settings page / routes
	settings.Register(a, r)

	return r
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'self'")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		next.ServeHTTP(w, r)
	})
}

func httpsRedirect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Forwarded-Proto") == "http" || (r.TLS == nil && r.Header.Get("X-Forwarded-Proto") == "") {
			if r.Host != "localhost" && r.Host != "127.0.0.1" && r.Host != "" {
				target := "https://" + r.Host + r.URL.RequestURI()
				http.Redirect(w, r, target, http.StatusSeeOther)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
