// --- FILE service.https ---

package commands

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sprout/internal/app"
	"sprout/internal/build"
	"sprout/internal/layout"
	"sprout/internal/platform/database"
	"sprout/internal/platform/database/config"
	"sprout/internal/types"
	"sprout/pkg/xlog"

	"github.com/urfave/cli/v3"
)

func serviceTestApp(t *testing.T) *app.App {
	t.Helper()
	tmp := t.TempDir()
	logger, err := xlog.New(filepath.Join(tmp, "logs"), "error")
	if err != nil {
		t.Fatal(err)
	}
	info := build.BuildInfo{
		Name:               "sprout",
		DefaultLogLevel:    "warn",
		ServiceEnabled:     true,
		ServiceDefaultPort: 8484,
	}
	db, err := database.New(filepath.Join(tmp, "db"), logger, info, database.ApplyPendingMigrations)
	if err != nil {
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
	a.Layout = layout.FromStorage(tmp, info.Name)
	if err := os.Mkdir(a.Layout.Control, 0o700); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestServiceBeforeAppliesRunPortOverrideAndPreservesHost(t *testing.T) {
	a := serviceTestApp(t)
	if _, err := config.Update(a.DB, func(cfg *types.Configuration) error {
		cfg.UIBind = "127.0.0.1:8484"
		return nil
	}); err != nil {
		t.Fatalf("configure loopback bind: %v", err)
	}
	cfg, err := serviceRunConfiguration(a, 9191)
	if err != nil {
		t.Fatalf("prepare service run configuration: %v", err)
	}
	if cfg.UIBind != "127.0.0.1:9191" {
		t.Fatalf("overridden UI bind = %q, want 127.0.0.1:9191", cfg.UIBind)
	}

	command := serviceCommand(a)
	run := command.Command("run")
	if run == nil {
		t.Fatal("service run command is missing")
	}
	run.Action = func(context.Context, *cli.Command) error {
		if a.BaseURL != "https://localhost:9191" {
			t.Fatalf("BaseURL = %q, want https://localhost:9191", a.BaseURL)
		}
		return nil
	}

	if err := command.Run(context.Background(), []string{"service", "run", "--port", "9191"}); err != nil {
		t.Fatalf("run service command: %v", err)
	}
}

func TestServiceRunRejectsConcurrentInstanceBeforeStartingComponents(t *testing.T) {
	a := serviceTestApp(t)
	lock, err := a.AcquireServiceLock()
	if err != nil {
		t.Fatalf("acquire first service lock: %v", err)
	}
	defer lock.Close()

	command := serviceCommand(a)
	err = command.Run(context.Background(), []string{"service", "run"})
	if !errors.Is(err, app.ErrServiceAlreadyRunning) {
		t.Fatalf("service run error = %v, want ErrServiceAlreadyRunning", err)
	}
}

func TestRunServiceCoordinatesReadinessAndCancellation(t *testing.T) {
	a := serviceTestApp(t)
	oldWorker, oldHTTP := runWorkerComponent, runHTTPComponent
	t.Cleanup(func() {
		runWorkerComponent, runHTTPComponent = oldWorker, oldHTTP
	})

	started := make(chan string, 2)
	runWorkerComponent = func(ctx context.Context, _ *app.App, ready func()) error {
		ready()
		started <- "worker"
		<-ctx.Done()
		return nil
	}
	runHTTPComponent = func(ctx context.Context, _ *app.App) error {
		started <- "dashboard"
		<-ctx.Done()
		return nil
	}

	httpReady := make(chan struct{}, 1)
	httpReady <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runService(ctx, a, httpReady) }()

	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("component did not start")
		}
	}

	deadline := time.Now().Add(time.Second)
	for {
		cfg, err := config.View(a.DB)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.StartCounter == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("StartCounter = %d, want 1", cfg.StartCounter)
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runService: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runService did not stop")
	}
}

func TestRunServiceWaitsForComponentReadiness(t *testing.T) {
	a := serviceTestApp(t)
	oldWorker, oldHTTP := runWorkerComponent, runHTTPComponent
	t.Cleanup(func() {
		runWorkerComponent, runHTTPComponent = oldWorker, oldHTTP
	})

	workerStarted := make(chan struct{})
	allowWorkerReady := make(chan struct{})
	runWorkerComponent = func(ctx context.Context, _ *app.App, ready func()) error {
		close(workerStarted)
		select {
		case <-allowWorkerReady:
		case <-ctx.Done():
			return nil
		}
		ready()
		<-ctx.Done()
		return nil
	}
	runHTTPComponent = func(ctx context.Context, _ *app.App) error {
		<-ctx.Done()
		return nil
	}

	httpReady := make(chan struct{}, 1)
	httpReady <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runService(ctx, a, httpReady) }()

	select {
	case <-workerStarted:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		cfg, err := config.View(a.DB)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.StartCounter != 0 {
			t.Fatalf("StartCounter = %d before worker readiness, want 0", cfg.StartCounter)
		}
		time.Sleep(5 * time.Millisecond)
	}

	close(allowWorkerReady)
	deadline = time.Now().Add(time.Second)
	for {
		cfg, err := config.View(a.DB)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.StartCounter == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("StartCounter = %d after worker readiness, want 1", cfg.StartCounter)
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runService: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runService did not stop")
	}
}

func TestRunServiceCancelsSiblingOnFailure(t *testing.T) {
	a := serviceTestApp(t)
	oldWorker, oldHTTP := runWorkerComponent, runHTTPComponent
	t.Cleanup(func() {
		runWorkerComponent, runHTTPComponent = oldWorker, oldHTTP
	})

	want := errors.New("worker failed")
	fail := make(chan struct{})
	httpCanceled := make(chan struct{})
	runWorkerComponent = func(context.Context, *app.App, func()) error {
		<-fail
		return want
	}
	runHTTPComponent = func(ctx context.Context, _ *app.App) error {
		<-ctx.Done()
		close(httpCanceled)
		return nil
	}

	httpReady := make(chan struct{}, 1)
	httpReady <- struct{}{}
	done := make(chan error, 1)
	go func() { done <- runService(context.Background(), a, httpReady) }()
	close(fail)

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), want.Error()) {
			t.Fatalf("runService error = %v, want worker failure", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runService did not return after worker failure")
	}
	select {
	case <-httpCanceled:
	default:
		t.Fatal("dashboard sibling was not canceled")
	}
	cfg, err := config.View(a.DB)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StartCounter != 0 {
		t.Fatalf("StartCounter = %d, want 0 after startup failure", cfg.StartCounter)
	}
}

func TestBindToBaseURLUsesInjectedDefault(t *testing.T) {
	if got := bindToBaseURL("", 9191); got != "https://localhost:9191" {
		t.Fatalf("default URL = %q, want https://localhost:9191", got)
	}
	if got := bindToBaseURL("127.0.0.1:8484", 9191); got != "https://localhost:8484" {
		t.Fatalf("bound URL = %q, want https://localhost:8484", got)
	}
}
