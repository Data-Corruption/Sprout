package maintenance

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sprout/internal/layout"
)

const testNonce = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func testLayout(t *testing.T) layout.Layout {
	t.Helper()
	l := layout.FromStorage(filepath.Join(t.TempDir(), "storage"), "sprout")
	if err := l.Ensure(); err != nil {
		t.Fatalf("ensure test layout: %v", err)
	}
	return l
}

func readyState() State {
	return State{
		Phase:             PhaseReady,
		Version:           "v1.2.3",
		ChangedAt:         "2026-08-28T17:00:00.123Z",
		InstallationEpoch: testNonce,
	}
}

func TestStateRoundTripAndAtomicReplacement(t *testing.T) {
	l := testLayout(t)
	first := readyState()
	if err := WriteState(l, first); err != nil {
		t.Fatalf("write first state: %v", err)
	}
	got, err := ReadState(l)
	if err != nil {
		t.Fatalf("read first state: %v", err)
	}
	if got != first {
		t.Fatalf("state = %#v, want %#v", got, first)
	}

	second := first
	second.Version = "v1.2.4"
	second.ChangedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := WriteState(l, second); err != nil {
		t.Fatalf("replace state: %v", err)
	}
	got, err = ReadState(l)
	if err != nil || got != second {
		t.Fatalf("replaced state = %#v, %v; want %#v", got, err, second)
	}
	matches, err := filepath.Glob(filepath.Join(l.Control, ".state-*.tmp"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary states remain: %v, %v", matches, err)
	}
}

func TestReadStateRejectsUnknownAndTrailingJSON(t *testing.T) {
	l := testLayout(t)
	valid := `{"phase":"ready","version":"v1.2.3","targetVersion":"","nonce":"","changedAt":"2026-08-28T17:00:00Z","installationEpoch":"epoch"}`
	for _, data := range []string{
		strings.TrimSuffix(valid, "}") + `,"unexpected":true}`,
		valid + `{}`,
	} {
		if err := os.WriteFile(l.State, []byte(data), 0o600); err != nil {
			t.Fatalf("write invalid state: %v", err)
		}
		if _, err := ReadState(l); err == nil {
			t.Fatalf("invalid state accepted: %s", data)
		}
	}
}

func TestStatePhaseValidation(t *testing.T) {
	tests := []struct {
		name  string
		state State
		want  string
	}{
		{"ready target", State{Phase: PhaseReady, Version: "v1.2.3", TargetVersion: "v1.2.4", ChangedAt: "2026-08-28T17:00:00Z", InstallationEpoch: "epoch"}, "must not contain"},
		{"update missing nonce", State{Phase: PhaseUpdating, Version: "v1.2.3", TargetVersion: "v1.2.4", ChangedAt: "2026-08-28T17:00:00Z", InstallationEpoch: "epoch"}, "requires"},
		{"uninstall target", State{Phase: PhaseUninstalling, Version: "v1.2.3", TargetVersion: "v1.2.4", ChangedAt: "2026-08-28T17:00:00Z", InstallationEpoch: "epoch"}, "must not contain"},
		{"uninstalled version", State{Phase: PhaseUninstalled, Version: "v1.2.3", ChangedAt: "2026-08-28T17:00:00Z", InstallationEpoch: "epoch"}, "must not contain"},
		{"bad timestamp", State{Phase: PhaseReady, Version: "v1.2.3", ChangedAt: "yesterday", InstallationEpoch: "epoch"}, "RFC3339"},
		{"bad epoch", State{Phase: PhaseReady, Version: "v1.2.3", ChangedAt: "2026-08-28T17:00:00Z"}, "installationEpoch"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.state.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestExpectationChecksSelectedFields(t *testing.T) {
	state := readyState()
	if err := (Expectation{Phase: PhaseReady, Version: state.Version, InstallationEpoch: state.InstallationEpoch}).Check(state); err != nil {
		t.Fatalf("matching expectation: %v", err)
	}
	if err := (Expectation{InstallationEpoch: "different"}).Check(state); err == nil {
		t.Fatal("stale epoch accepted")
	}
	if err := (Expectation{Version: "v9.9.9"}).Check(state); err == nil {
		t.Fatal("wrong version accepted")
	}
}

func TestEnsureDevReadyBootstrapsAndReconciles(t *testing.T) {
	l := testLayout(t)
	state, err := EnsureDevReady(l, "v1.2.3")
	if err != nil {
		t.Fatalf("bootstrap dev state: %v", err)
	}
	if state.Phase != PhaseReady || state.Version != "v1.2.3" || state.InstallationEpoch == "" {
		t.Fatalf("bootstrapped state = %#v", state)
	}
	epoch := state.InstallationEpoch
	state, err = EnsureDevReady(l, "v1.2.4")
	if err != nil {
		t.Fatalf("reconcile dev state: %v", err)
	}
	if state.Version != "v1.2.4" || state.InstallationEpoch != epoch {
		t.Fatalf("reconciled state = %#v, want version change and stable epoch", state)
	}
	state.Phase = PhaseUninstalled
	state.Version = ""
	state.ChangedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := WriteState(l, state); err != nil {
		t.Fatalf("write uninstalled dev state: %v", err)
	}
	state, err = EnsureDevReady(l, "v1.2.4")
	if err != nil {
		t.Fatalf("reinstall dev state: %v", err)
	}
	if state.InstallationEpoch == epoch {
		t.Fatal("dev reinstall retained old installation epoch")
	}
}

func TestReadStateReportsMissingFile(t *testing.T) {
	l := testLayout(t)
	_, err := ReadState(l)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing state error = %v, want os.ErrNotExist", err)
	}
}
