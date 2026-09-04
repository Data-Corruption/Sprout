// --- FILE service.https ---

package router

import (
	"errors"
	"net/http"
	"sprout/internal/app"
	"sprout/internal/platform/database/config"
	"sprout/internal/platform/http/cookies"
	"sprout/internal/platform/http/middleware"
	"time"

	"github.com/go-chi/chi/v5"
	"sprout/pkg/xhttp"
)

func RegisterLoginRoutes(a *app.App, auth *middleware.AuthService, r chi.Router) {
	r.Get("/login", handleGetLogin(a))
	r.Post("/login", handlePostLogin(a, auth))
}

func loginData(a *app.App) map[string]any {
	data := a.UI.PageData("Login", a.BuildInfo().Version)
	// First-run hint: with no credentials the form is a dead end, so tell the
	// user how to create one instead.
	if cfg, err := config.View(a.DB); err == nil && len(cfg.Credentials) == 0 {
		data["NoCredentials"] = true
		data["AppName"] = a.BuildInfo().Name
	}
	return data
}

func handleGetLogin(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := a.UI.Execute(w, "login.html", loginData(a)); err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}
	}
}

func handlePostLogin(a *app.App, auth *middleware.AuthService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.FormValue("username")
		password := r.FormValue("password")

		token, errMsg, err := auth.NewSession(username, password)
		if err != nil {
			a.Log.Warnf("login failed from %s: %v", r.RemoteAddr, err)
			data := loginData(a)
			data["Error"] = errMsg
			data["Username"] = username
			status := http.StatusUnauthorized
			if errors.Is(err, middleware.ErrTooManyRequests) {
				status = http.StatusTooManyRequests
			}
			w.WriteHeader(status)
			if err := a.UI.Execute(w, "login.html", data); err != nil {
				xhttp.Error(r.Context(), w, err)
			}
			return
		}

		a.Log.Infof("login succeeded from %s", r.RemoteAddr)
		secureCookie := !a.DevMode
		cookieName := middleware.SessionCookieName(a.BuildInfo().Name)
		cookies.Set(w, cookieName, token, "/", middleware.SessionDuration, secureCookie)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

func handleLogout(a *app.App, auth *middleware.AuthService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookieName := middleware.SessionCookieName(a.BuildInfo().Name)
		token := cookies.Read(r, cookieName)
		if err := auth.DeleteSession(token); err != nil {
			a.Log.Errorf("failed to delete session on logout: %v", err)
		}
		secureCookie := !a.DevMode
		cookies.Set(w, cookieName, "", "/", -time.Second, secureCookie)
		a.Log.Infof("logout from %s", r.RemoteAddr)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}
}
