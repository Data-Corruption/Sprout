//go:build windows

// --- FILE service ---

package app

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"sprout/internal/layout"
)

func TestParseServiceStopLease(t *testing.T) {
	want := time.UnixMilli(1_700_000_000_123)
	got, err := parseServiceStopLease([]byte(strconv.FormatInt(want.UnixMilli(), 10) + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(want) {
		t.Fatalf("expiry = %v, want %v", got, want)
	}

	if _, err := parseServiceStopLease([]byte("not-a-time")); err == nil {
		t.Fatal("malformed lease was accepted")
	}
}

func TestReadServiceStopLeaseIgnoresExpiredFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ServiceStopLeaseFileName)
	expired := time.Now().Add(-time.Second)
	if err := os.WriteFile(path, []byte(strconv.FormatInt(expired.UnixMilli(), 10)), 0o600); err != nil {
		t.Fatal(err)
	}

	active, err := readServiceStopLease(path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("expired lease reported active")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expired lease was unexpectedly removed: %v", err)
	}
}

func TestReleaseServiceStopRequestOnlyRetiresOwnLease(t *testing.T) {
	a := &App{Layout: layout.FromStorage(t.TempDir(), "sprout")}
	if err := a.ReleaseServiceStopRequest(); err != nil {
		t.Fatalf("release without a request: %v", err)
	}
	if _, err := os.Stat(a.Layout.ServiceStop); !os.IsNotExist(err) {
		t.Fatalf("release without a request touched the lease file: %v", err)
	}

	if err := a.RequestServiceStop(); err != nil {
		t.Fatal(err)
	}
	owned := a.ServiceStopLease()
	if len(owned) == 0 {
		t.Fatal("request did not record the written lease")
	}

	// A second controller replaces the lease with a newer request before the
	// first controller finishes; the first must leave it alone.
	newer := []byte(strconv.FormatInt(time.Now().Add(2*ServiceStopLeaseDuration).UnixMilli(), 10) + "\n")
	if err := os.WriteFile(a.Layout.ServiceStop, newer, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.ReleaseServiceStopRequest(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(a.Layout.ServiceStop); string(data) != string(newer) {
		t.Fatalf("release clobbered a newer lease: %q", data)
	}
	if a.ServiceStopLease() != nil {
		t.Fatal("release did not forget the retired lease")
	}

	// The common case: nobody else wrote, so the lease is tombstoned.
	if err := a.RequestServiceStop(); err != nil {
		t.Fatal(err)
	}
	if err := a.ReleaseServiceStopRequest(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(a.Layout.ServiceStop); string(data) != serviceStopTombstone {
		t.Fatalf("release did not tombstone its own lease: %q", data)
	}
}

func TestRunServiceStopWatcherCancelsContextAndReturns(t *testing.T) {
	a := &App{Layout: layout.FromStorage(t.TempDir(), "sprout")}
	if err := a.RequestServiceStop(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() { done <- a.RunServiceStopWatcher(ctx, cancel, func() { ready <- struct{}{} }) }()

	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("stop lease did not cancel the service context")
	}
	if err := <-done; err != nil {
		t.Fatalf("watcher returned: %v", err)
	}
	select {
	case <-ready:
		t.Fatal("watcher announced readiness despite a pre-existing stop request")
	default:
	}
}
