// --- FILE service.https ---

package settings

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sprout/internal/app"

	"sprout/internal/maintenance"
	"sprout/internal/platform/database/config"
	"sprout/internal/platform/http/middleware"
	"sprout/internal/types"

	"sprout/pkg/xhttp"

	"github.com/go-chi/chi/v5"
)

func Register(a *app.App, r chi.Router) {
	r.Get("/", handleGetSettings(a))
	r.Post("/settings", handleUpdateSettings(a))
	r.Post("/settings/stop", handleStop(a))
	r.Post("/settings/restart", handleRestart(a))
	// --- BEGIN update.self ---
	r.Post("/settings/update", handleUpdate(a))
	// --- END update.self ---
	r.Get("/settings/restart-status", handleRestartStatus(a))
}

func handleGetSettings(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, err := config.View(a.DB)
		if err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}

		data := a.UI.PageData("Settings", a.BuildInfo().Version)
		// --- BEGIN update.notifications ---
		data["UpdateAvailable"] = cfg.UpdateAvailable && !a.DevMode
		// --- END update.notifications ---
		data["LogLevel"] = cfg.LogLevel
		data["UIBind"] = cfg.UIBind
		data["ProxyBind"] = cfg.ProxyBind
		if err := a.UI.Execute(w, "settings.html", data); err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}
	}
}

func handleUpdateSettings(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := middleware.RequirePerm(r, types.PermSettings); err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}

		// Parse body - all fields are optional
		var body struct {
			LogLevel  *string `json:"logLevel"`
			UIBind    *string `json:"uiBind"`
			ProxyBind *string `json:"proxyBind"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
		dec := json.NewDecoder(r.Body)
		if err := dec.Decode(&body); err != nil {
			xhttp.Error(r.Context(), w, &xhttp.Err{Code: 400, Msg: "bad request", Err: err})
			return
		}

		// Update only the fields that were provided. Validation happens inside
		// config.Update (all writers get it); violations surface as 400s.
		if _, err := config.Update(a.DB, func(cfg *types.Configuration) error {
			if body.LogLevel != nil {
				cfg.LogLevel = *body.LogLevel
			}
			if body.UIBind != nil {
				cfg.UIBind = *body.UIBind
			}
			if body.ProxyBind != nil {
				cfg.ProxyBind = *body.ProxyBind
			}
			return nil
		}); err != nil {
			writeConfigUpdateError(r, w, err)
			return
		}

		w.WriteHeader(http.StatusOK)
	}
}

// maxJSONBody caps JSON request bodies; the settings payloads are tiny.
const maxJSONBody = 64 << 10 // 64 KiB

func writeConfigUpdateError(r *http.Request, w http.ResponseWriter, err error) {
	var validationErr *config.ValidationError
	if errors.As(err, &validationErr) {
		xhttp.Error(r.Context(), w, &xhttp.Err{
			Code: http.StatusBadRequest,
			Msg:  validationErr.Error(),
			Err:  err,
		})
		return
	}
	xhttp.Error(r.Context(), w, err)
}

func handleStop(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := middleware.RequirePerm(r, types.PermServerControl); err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		requestServiceStop(a) // platform-specific, see settings_control_*.go
	}
}

// prepRestart resets the restart detector. Sessions live in the DB, so the
// browser that triggered the restart stays logged in with no extra handoff.
func prepRestart(a *app.App) error {
	_, err := config.Update(a.DB, func(cfg *types.Configuration) error {
		cfg.StartCounter = 0
		return nil
	})
	return err
}

func handleRestart(a *app.App) http.HandlerFunc {
	return handleRestartWith(a, func() {
		requestServiceRestart(a)
	})
}

func handleRestartWith(a *app.App, restart func()) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := middleware.RequirePerm(r, types.PermServerControl); err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}

		a.Log.Debug("Restart requested.")

		// reset StartCounter (post migrate restart will increment)
		if err := prepRestart(a); err != nil {
			xhttp.Error(r.Context(), w, &xhttp.Err{Code: 500, Msg: "failed to update config", Err: err})
			return
		}

		w.WriteHeader(http.StatusAccepted)
		restart()
	}
}

// --- BEGIN update.self ---
func handleUpdate(a *app.App) http.HandlerFunc {
	checkForUpdate := a.CheckForUpdate
	// --- BEGIN update.notifications ---
	checkForUpdate = a.CheckForUpdateAndNotify
	// --- END update.notifications ---
	return handleUpdateWith(a, checkForUpdate, func() error {
		_, err := a.StartMaintenance(a.Context, maintenance.ActionUpdate)
		return err
	})
}

func handleUpdateWith(
	a *app.App,
	checkForUpdate func(context.Context) (bool, error),
	startUpdate func() error,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := middleware.RequirePerm(r, types.PermServerControl); err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}

		updateAvailable, err := checkForUpdate(r.Context())
		if err != nil {
			switch {
			case errors.Is(err, app.ErrUpdatesDisabled):
				xhttp.Error(r.Context(), w, &xhttp.Err{
					Code: http.StatusConflict,
					Msg:  "This installation does not manage updates. " + app.UpdateGuidance,
					Err:  err,
				})
			case errors.Is(err, app.ErrDevBuild):
				xhttp.Error(r.Context(), w, &xhttp.Err{
					Code: http.StatusConflict,
					Msg:  "Updates are unavailable for development builds.",
					Err:  err,
				})
			default:
				xhttp.Error(r.Context(), w, &xhttp.Err{
					Code: http.StatusBadGateway,
					Msg:  "The update check failed.",
					Err:  err,
				})
			}
			return
		}
		if !updateAvailable {
			writeJSON(w, http.StatusOK, map[string]string{
				"status":  "current",
				"message": "Already running the latest version.",
			})
			return
		}

		if err := prepRestart(a); err != nil {
			xhttp.Error(r.Context(), w, &xhttp.Err{
				Code: http.StatusInternalServerError,
				Msg:  "Failed to prepare update tracking.",
				Err:  err,
			})
			return
		}
		if err := startUpdate(); err != nil {
			switch {
			case errors.Is(err, app.ErrUpdatesDisabled):
				xhttp.Error(r.Context(), w, &xhttp.Err{
					Code: http.StatusConflict,
					Msg:  "This installation does not manage updates. " + app.UpdateGuidance,
					Err:  err,
				})
			default:
				xhttp.Error(r.Context(), w, &xhttp.Err{
					Code: http.StatusInternalServerError,
					Msg:  "The updater could not be started.",
					Err:  err,
				})
			}
			return
		}

		writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
	}
}

// --- END update.self ---

func handleRestartStatus(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := middleware.RequirePerm(r, types.PermServerControl); err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}
		cfg, err := config.View(a.DB)
		if err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}

		restarted := cfg.StartCounter > 0
		resp := map[string]bool{"restarted": restarted}
		a.Log.Debugf("Restart status check: StartCounter=%d, Restarted=%t", cfg.StartCounter, restarted)

		// --- BEGIN update.self ---
		updated := cfg.LastShutdownVersion != "" && cfg.LastShutdownVersion != a.BuildInfo().Version
		resp["updated"] = updated
		a.Log.Debugf("Restart status check: LastShutdownVersion=%q, CurrentVersion=%q, Updated=%t",
			cfg.LastShutdownVersion, a.BuildInfo().Version, updated)
		// --- END update.self ---

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			xhttp.Error(r.Context(), w, err)
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
