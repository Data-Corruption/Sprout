//go:build !windows

// --- FILE update ---

package app

// --- BEGIN update.apply ---
// --- BEGIN update.apply.auto ---

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sprout/internal/maintenance"
	"sprout/internal/platform/database/config"
	"sprout/internal/types"
)

func TestServiceConsumesOneShotThatCompletesWhileItWaits(t *testing.T) {
	source := &blockingReleaseSource{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	a := newUpdateTestApp(t, source)
	a.buildInfo.Name = "sprout"
	a.buildInfo.CertIdentity = "test-identity"
	a.buildInfo.OidcIssuer = "test-issuer"
	defer a.Close()

	if err := os.WriteFile(a.Layout.ReleaseURL, []byte("file:///missing-release/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := maintenance.WriteState(a.Layout, maintenance.State{
		Phase:             maintenance.PhaseReady,
		Version:           a.buildInfo.Version,
		ChangedAt:         time.Now().UTC().Format(time.RFC3339Nano),
		InstallationEpoch: "test-installation-epoch",
	}); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "cosign"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("NOTIFY_SOCKET", "")

	if _, err := config.Update(a.DB, func(cfg *types.Configuration) error {
		cfg.UpdateNotifications = true
		cfg.LatestUpdateVersion = ""
		cfg.AutomaticUpdates = true
		cfg.LastUpdateCheck = time.Time{}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.StartUpdateCheckIfDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-source.started:
	case <-time.After(time.Second):
		t.Fatal("one-shot update check did not reach the release source")
	}
	a.maintenanceMu.Lock()
	startedByOneShot := a.maintenanceStarted
	a.maintenanceMu.Unlock()
	if startedByOneShot {
		t.Fatal("one-shot update check launched maintenance")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.RunUpdateChecker(ctx, func() {}) }()
	// Give the service iteration time to contend on the in-process check mutex
	// before the one-shot publishes its result. This is the handoff ordering
	// that previously let the service sleep until the next daily interval. A
	// blocked Lock is not observable, so this is best effort: if the service
	// arrives late it reads the persisted result instead, and the assertions
	// below hold under either ordering.
	time.Sleep(100 * time.Millisecond)
	close(source.release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		a.maintenanceMu.Lock()
		started := a.maintenanceStarted
		a.maintenanceMu.Unlock()
		if started {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("service did not consume fresh availability")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("service update checker did not stop")
	}
}

// --- END update.apply.auto ---
// --- END update.apply ---
