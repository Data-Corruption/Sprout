// --- FILE update.notifications ---

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"sprout/internal/build"
	"sprout/internal/layout"
	"sprout/internal/maintenance"
	"sprout/internal/platform/database"
	"sprout/internal/platform/database/config"
	"sprout/internal/platform/database/updatelease"
	"sprout/pkg/xlog"
)

func TestCheckForUpdateAndNotifyPersistsResult(t *testing.T) {
	tmp := t.TempDir()
	if err := os.Chmod(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	logger, err := xlog.New(filepath.Join(tmp, "logs"), "error")
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	buildInfo := build.Info()
	buildInfo.Version = "v1.0.0"
	db, err := database.New(filepath.Join(tmp, "db"), logger, buildInfo, database.ApplyPendingMigrations)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	appLayout := layout.FromStorage(tmp, "sprout")
	if err := appLayout.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(appLayout.ReleaseURL, []byte("https://example.invalid/"), 0600); err != nil {
		t.Fatal(err)
	}

	a := &App{
		DB:            db,
		Log:           logger,
		Layout:        appLayout,
		ReleaseSource: &MockReleaseSource{LatestVersion: "v1.1.0"},
		buildInfo:     buildInfo,
	}
	available, err := a.CheckForUpdateAndNotify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !available {
		t.Fatal("expected update to be available")
	}

	cfg, err := config.View(db)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.UpdateAvailable || cfg.LastUpdateCheck.IsZero() {
		t.Fatalf("notification state not persisted: %+v", cfg)
	}
}

type updateTestReleaseSource interface {
	GetLatestVersion(context.Context, string) (string, error)
}

func newUpdateNotificationTestApp(t *testing.T, source updateTestReleaseSource) *App {
	t.Helper()
	tmp := t.TempDir()
	if err := os.Chmod(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	logger, err := xlog.New(filepath.Join(tmp, "logs"), "error")
	if err != nil {
		t.Fatal(err)
	}
	buildInfo := build.Info()
	buildInfo.Version = "v1.0.0"
	db, err := database.New(filepath.Join(tmp, "db"), logger, buildInfo, database.ApplyPendingMigrations)
	if err != nil {
		logger.Close()
		t.Fatal(err)
	}
	appLayout := layout.FromStorage(tmp, "sprout")
	if err := appLayout.Ensure(); err != nil {
		db.Close()
		logger.Close()
		t.Fatal(err)
	}
	if err := os.WriteFile(appLayout.ReleaseURL, []byte("https://example.invalid/"), 0600); err != nil {
		db.Close()
		logger.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		logger.Close()
	})
	return &App{
		DB:            db,
		Log:           logger,
		Layout:        appLayout,
		ReleaseSource: source,
		buildInfo:     buildInfo,
	}
}

func TestManualUpdateCheckIgnoresPeriodicLease(t *testing.T) {
	a := newUpdateNotificationTestApp(t, &MockReleaseSource{LatestVersion: "v1.1.0"})
	now := time.Now()
	if _, claimed, err := updatelease.Claim(
		context.Background(),
		a.DB,
		now,
		time.Minute,
	); err != nil {
		t.Fatal(err)
	} else if !claimed {
		t.Fatal("failed to establish periodic lease")
	}

	available, err := a.CheckForUpdateAndNotify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !available {
		t.Fatal("manual check did not run while periodic lease was held")
	}
	if _, claimed, err := updatelease.Claim(
		context.Background(),
		a.DB,
		now.Add(time.Second),
		time.Minute,
	); err != nil {
		t.Fatal(err)
	} else if claimed {
		t.Fatal("manual check removed the periodic lease")
	}
}

func TestFailedPeriodicUpdateCheckLeavesLease(t *testing.T) {
	a := newUpdateNotificationTestApp(t, &MockReleaseSource{Error: context.DeadlineExceeded})
	if _, _, err := a.checkForPeriodicUpdate(context.Background()); err == nil {
		t.Fatal("periodic check unexpectedly succeeded")
	}
	if _, claimed, err := updatelease.Claim(
		context.Background(),
		a.DB,
		time.Now(),
		time.Minute,
	); err != nil {
		t.Fatal(err)
	} else if claimed {
		t.Fatal("failed periodic check released its lease")
	}
}

func TestServiceUpdateCheckerKeepsRunningWithInvalidMetadata(t *testing.T) {
	a := newUpdateNotificationTestApp(t, &MockReleaseSource{LatestVersion: "v1.1.0"})
	if err := os.WriteFile(a.Layout.ReleaseURL, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.RunUpdateChecker(ctx, func() {}) }()
	select {
	case err := <-done:
		t.Fatalf("checker stopped on optional metadata error: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("checker did not stop after cancellation")
	}
}

// --- BEGIN update.self ---
// --- BEGIN update.auto ---
func TestFinishedUpdateJobRestoresAvailabilityForRetry(t *testing.T) {
	a := newUpdateNotificationTestApp(t, &MockReleaseSource{LatestVersion: "v1.1.0"})
	if err := maintenance.WriteState(a.Layout, maintenance.State{
		Phase:             maintenance.PhaseReady,
		Version:           a.buildInfo.Version,
		ChangedAt:         time.Now().UTC().Format(time.RFC3339Nano),
		InstallationEpoch: "test-installation-epoch",
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.setUpdateAvailable(false); err != nil {
		t.Fatal(err)
	}

	a.maintenanceStarted = true
	a.maintenanceAction = maintenance.ActionUpdate
	a.maintenanceJob = filepath.Join(a.Layout.Jobs, "finished-job")
	a.maintenanceLog = a.Layout.MaintenanceLog
	running, err := a.refreshMaintenanceLocked()
	if err != nil {
		t.Fatal(err)
	}
	if running || a.maintenanceStarted {
		t.Fatal("finished maintenance job remained cached")
	}
	cfg, err := config.View(a.DB)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.UpdateAvailable {
		t.Fatal("failed update was not made eligible for retry")
	}
}

// TestOrphanedUpdateJobIsReapedAndRetried covers a runner that died without
// removing its directory: once the start grace has passed with no identity
// published, the admitting process reaps the directory itself and treats the
// job like any other pre-commit failure.
func TestOrphanedUpdateJobIsReapedAndRetried(t *testing.T) {
	a := newUpdateNotificationTestApp(t, &MockReleaseSource{LatestVersion: "v1.1.0"})
	if err := maintenance.WriteState(a.Layout, maintenance.State{
		Phase:             maintenance.PhaseReady,
		Version:           a.buildInfo.Version,
		ChangedAt:         time.Now().UTC().Format(time.RFC3339Nano),
		InstallationEpoch: "test-installation-epoch",
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.setUpdateAvailable(false); err != nil {
		t.Fatal(err)
	}
	jobDir := filepath.Join(a.Layout.Jobs, "orphaned-job")
	if err := os.MkdirAll(jobDir, 0o700); err != nil {
		t.Fatal(err)
	}
	a.maintenanceStarted = true
	a.maintenanceAction = maintenance.ActionUpdate
	a.maintenanceJob = jobDir
	a.maintenanceLog = a.Layout.MaintenanceLog

	a.maintenanceAdmitted = time.Now()
	running, err := a.refreshMaintenanceLocked()
	if err != nil {
		t.Fatal(err)
	}
	if !running {
		t.Fatal("freshly admitted job without identity was not treated as running")
	}

	a.maintenanceAdmitted = time.Now().Add(-time.Hour)
	running, err = a.refreshMaintenanceLocked()
	if err != nil {
		t.Fatal(err)
	}
	if running || a.maintenanceStarted {
		t.Fatal("orphaned maintenance job remained cached")
	}
	if _, err := os.Stat(jobDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphaned job directory was not reaped: %v", err)
	}
	cfg, err := config.View(a.DB)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.UpdateAvailable {
		t.Fatal("orphaned update was not made eligible for retry")
	}
}

// --- END update.auto ---
// --- END update.self ---

// --- BEGIN update.self ---
// --- BEGIN update.auto ---
type blockingReleaseSource struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func TestCloseCancelsAndJoinsOneShotUpdateCheck(t *testing.T) {
	source := &blockingReleaseSource{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	a := newUpdateNotificationTestApp(t, source)
	if err := a.StartUpdateCheckIfDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-source.started:
	case <-time.After(time.Second):
		t.Fatal("one-shot check did not start")
	}

	done := make(chan error, 1)
	go func() { done <- a.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel and join the one-shot check")
	}
	if calls := source.calls.Load(); calls != 1 {
		t.Fatalf("release source calls = %d, want 1", calls)
	}
}

func (s *blockingReleaseSource) GetLatestVersion(ctx context.Context, _ string) (string, error) {
	s.calls.Add(1)
	s.started <- struct{}{}
	select {
	case <-s.release:
		return "v1.1.0", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func TestContendingPeriodicChecksLaunchOneAutomaticUpdate(t *testing.T) {
	source := &blockingReleaseSource{
		started: make(chan struct{}, 2),
		release: make(chan struct{}),
	}
	first := newUpdateNotificationTestApp(t, source)
	second := &App{
		DB:            first.DB,
		Log:           first.Log,
		Layout:        first.Layout,
		ReleaseSource: source,
		buildInfo:     first.buildInfo,
	}

	type result struct {
		completed bool
		err       error
	}
	results := make(chan result, 2)
	var launches atomic.Int32
	run := func(a *App) {
		available, completed, err := a.checkForPeriodicUpdate(context.Background())
		if err == nil && completed {
			err = maybeStartAutomaticUpdate(available, func() error {
				launches.Add(1)
				return nil
			})
		}
		results <- result{completed: completed, err: err}
	}

	go run(first)
	select {
	case <-source.started:
	case <-time.After(time.Second):
		t.Fatal("first periodic check did not reach release source")
	}

	go run(second)
	select {
	case got := <-results:
		if got.err != nil {
			t.Fatal(got.err)
		}
		if got.completed {
			t.Fatal("contending periodic check completed while lease was held")
		}
	case <-time.After(time.Second):
		close(source.release)
		t.Fatal("contending periodic check did not return")
	}

	close(source.release)
	select {
	case got := <-results:
		if got.err != nil {
			t.Fatal(got.err)
		}
		if !got.completed {
			t.Fatal("lease owner did not complete")
		}
	case <-time.After(time.Second):
		t.Fatal("lease owner did not return")
	}

	if calls := source.calls.Load(); calls != 1 {
		t.Fatalf("release source calls = %d, want 1", calls)
	}
	if got := launches.Load(); got != 1 {
		t.Fatalf("automatic update launches = %d, want 1", got)
	}
	cfg, err := config.View(first.DB)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.UpdateAvailable || cfg.LastUpdateCheck.IsZero() {
		t.Fatalf("periodic result not persisted: %+v", cfg)
	}
}

func TestMaybeStartAutomaticUpdate(t *testing.T) {
	t.Run("current does not launch", func(t *testing.T) {
		launches := 0
		err := maybeStartAutomaticUpdate(false, func() error {
			launches++
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if launches != 0 {
			t.Fatalf("launches = %d, want 0", launches)
		}
	})

	t.Run("newer launches detached updater", func(t *testing.T) {
		launches := 0
		err := maybeStartAutomaticUpdate(true, func() error {
			launches++
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if launches != 1 {
			t.Fatalf("launches = %d, want 1", launches)
		}
	})

	t.Run("launch failure is returned for logging", func(t *testing.T) {
		want := errors.New("launch failed")
		err := maybeStartAutomaticUpdate(true, func() error { return want })
		if !errors.Is(err, want) {
			t.Fatalf("error = %v, want wrapped launch failure", err)
		}
	})
}

// --- END update.auto ---
// --- END update.self ---
