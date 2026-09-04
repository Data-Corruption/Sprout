//go:build windows

// --- FILE service.https ---

package commands

import (
	"context"
	"testing"
	"time"

	"sprout/internal/app"
	"sprout/internal/platform/database/config"
)

func TestRunServiceHonorsStopLeaseBeforeReadiness(t *testing.T) {
	a := serviceTestApp(t)
	oldWorker, oldHTTP := runWorkerComponent, runHTTPComponent
	t.Cleanup(func() {
		runWorkerComponent, runHTTPComponent = oldWorker, oldHTTP
	})

	runWorkerComponent = waitForReadyServiceCancellation
	runHTTPComponent = waitForServiceCancellation
	if err := a.RequestServiceStop(); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		done <- runService(context.Background(), a, make(chan struct{}))
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runService: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runService did not honor the pre-existing stop lease")
	}

	cfg, err := config.View(a.DB)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StartCounter != 0 {
		t.Fatalf("StartCounter = %d, want 0", cfg.StartCounter)
	}
}

func TestRunServiceHonorsStopLeaseAfterReadiness(t *testing.T) {
	a := serviceTestApp(t)
	oldWorker, oldHTTP := runWorkerComponent, runHTTPComponent
	t.Cleanup(func() {
		runWorkerComponent, runHTTPComponent = oldWorker, oldHTTP
	})

	runWorkerComponent = waitForReadyServiceCancellation
	runHTTPComponent = waitForServiceCancellation
	httpReady := make(chan struct{}, 1)
	httpReady <- struct{}{}

	done := make(chan error, 1)
	go func() {
		done <- runService(context.Background(), a, httpReady)
	}()
	waitForStartCounter(t, a, 1)

	if err := a.RequestServiceStop(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runService: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runService did not honor the stop lease")
	}
}

func waitForServiceCancellation(ctx context.Context, _ *app.App) error {
	<-ctx.Done()
	return nil
}

func waitForReadyServiceCancellation(ctx context.Context, _ *app.App, ready func()) error {
	ready()
	<-ctx.Done()
	return nil
}

func waitForStartCounter(t *testing.T, a *app.App, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		cfg, err := config.View(a.DB)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.StartCounter == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("StartCounter = %d, want %d", cfg.StartCounter, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
