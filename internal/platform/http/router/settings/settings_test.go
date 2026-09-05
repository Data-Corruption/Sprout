// --- FILE service.https ---

package settings

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"sprout/internal/app"
	"sprout/internal/build"
	"sprout/internal/platform/database"
	"sprout/internal/platform/database/config"
	"sprout/internal/platform/http/middleware"
	"sprout/internal/types"
	"sprout/pkg/xlog"
)

func newSettingsTestApp(t *testing.T) *app.App {
	t.Helper()
	tmp := t.TempDir()
	logger, err := xlog.New(filepath.Join(tmp, "logs"), "error")
	if err != nil {
		t.Fatal(err)
	}
	buildInfo := build.BuildInfo{
		Name:               "sprout",
		Version:            "v1.0.0",
		DefaultLogLevel:    "warn",
		ServiceDefaultPort: 8484,
	}
	db, err := database.New(filepath.Join(tmp, "db"), logger, buildInfo, database.ApplyPendingMigrations)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		_ = logger.Close()
	})
	a := app.New(buildInfo)
	a.DB = db
	a.Log = logger
	return a
}

func runAuthorized(handler http.Handler, request *http.Request) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	middleware.DevAuth()(handler).ServeHTTP(recorder, request)
	return recorder
}

func TestRestartRequiresPermission(t *testing.T) {
	a := newSettingsTestApp(t)
	called := false
	handler := handleRestartWith(a, func() { called = true })
	request := httptest.NewRequest(http.MethodPost, "/settings/restart", strings.NewReader("{}"))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
	if called {
		t.Fatal("restart called without permission")
	}
}

func TestRestartAcceptedAfterPreparation(t *testing.T) {
	a := newSettingsTestApp(t)
	if _, err := config.Update(a.DB, func(cfg *types.Configuration) error {
		cfg.StartCounter = 4
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	called := false
	handler := handleRestartWith(a, func() { called = true })
	request := httptest.NewRequest(http.MethodPost, "/settings/restart", strings.NewReader("{}"))
	recorder := runAuthorized(handler, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", recorder.Code, recorder.Body.String())
	}
	if !called {
		t.Fatal("restart was not requested")
	}
	cfg, err := config.View(a.DB)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StartCounter != 0 {
		t.Fatalf("StartCounter = %d, want 0", cfg.StartCounter)
	}
}

// --- BEGIN update.apply ---
func TestUpdateRequiresPermission(t *testing.T) {
	a := newSettingsTestApp(t)
	checks, launches := 0, 0
	handler := handleUpdateWith(
		a,
		func(context.Context) (bool, error) {
			checks++
			return true, nil
		},
		func() error {
			launches++
			return nil
		},
	)
	request := httptest.NewRequest(http.MethodPost, "/settings/update", strings.NewReader("{}"))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
	if checks != 0 || launches != 0 {
		t.Fatalf("checks=%d launches=%d, want 0/0", checks, launches)
	}
}

func TestUpdateAlreadyCurrentDoesNotLaunch(t *testing.T) {
	a := newSettingsTestApp(t)
	launches := 0
	handler := handleUpdateWith(
		a,
		func(context.Context) (bool, error) { return false, nil },
		func() error {
			launches++
			return nil
		},
	)
	request := httptest.NewRequest(http.MethodPost, "/settings/update", strings.NewReader("{}"))
	recorder := runAuthorized(handler, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	if launches != 0 {
		t.Fatalf("launches = %d, want 0", launches)
	}
	if !strings.Contains(recorder.Body.String(), `"current"`) {
		t.Fatalf("body = %q, want current result", recorder.Body.String())
	}
}

func TestUpdateDisabledUsesSourceNeutralGuidance(t *testing.T) {
	a := newSettingsTestApp(t)
	handler := handleUpdateWith(
		a,
		func(context.Context) (bool, error) { return false, app.ErrUpdatesDisabled },
		func() error { return nil },
	)
	request := httptest.NewRequest(http.MethodPost, "/settings/update", strings.NewReader("{}"))
	recorder := runAuthorized(handler, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), app.UpdateGuidance) {
		t.Fatalf("body = %q, want source-neutral guidance", recorder.Body.String())
	}
}

func TestUpdateLaunchFailureReachesClient(t *testing.T) {
	a := newSettingsTestApp(t)
	want := errors.New("launcher failed")
	handler := handleUpdateWith(
		a,
		func(context.Context) (bool, error) { return true, nil },
		func() error { return want },
	)
	request := httptest.NewRequest(http.MethodPost, "/settings/update", strings.NewReader("{}"))
	recorder := runAuthorized(handler, request)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "could not be started") {
		t.Fatalf("body = %q, want launch failure", recorder.Body.String())
	}
}

func TestUpdateAcceptedOnlyAfterLaunch(t *testing.T) {
	a := newSettingsTestApp(t)
	if _, err := config.Update(a.DB, func(cfg *types.Configuration) error {
		cfg.StartCounter = 2
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	launched := false
	handler := handleUpdateWith(
		a,
		func(context.Context) (bool, error) { return true, nil },
		func() error {
			launched = true
			return nil
		},
	)
	request := httptest.NewRequest(http.MethodPost, "/settings/update", strings.NewReader("{}"))
	recorder := runAuthorized(handler, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", recorder.Code, recorder.Body.String())
	}
	if !launched {
		t.Fatal("202 returned before launcher ran")
	}
	cfg, err := config.View(a.DB)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StartCounter != 0 {
		t.Fatalf("StartCounter = %d, want 0", cfg.StartCounter)
	}
}

// --- END update.apply ---
