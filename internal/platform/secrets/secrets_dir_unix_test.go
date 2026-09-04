//go:build !windows

// --- FILE service.https ---

package secrets

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewRejectsUnsafeSecretsDirectory(t *testing.T) {
	storage := t.TempDir()
	dir := filepath.Join(storage, "secrets")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir loose secrets dir: %v", err)
	}
	if _, err := New(t.Context(), storage, "test"); err == nil {
		t.Fatal("New accepted a secrets directory with mode 0755")
	}

	storage = t.TempDir()
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(storage, "secrets")); err != nil {
		t.Fatalf("create secrets symlink: %v", err)
	}
	if _, err := New(t.Context(), storage, "test"); err == nil {
		t.Fatal("New accepted a symlinked secrets directory")
	}
}

func TestNewRejectsLooseKeyInsteadOfRepairing(t *testing.T) {
	storage := t.TempDir()
	store, err := New(t.Context(), storage, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := os.Chmod(store.KeyPath(), 0o644); err != nil {
		t.Fatalf("loosen key mode: %v", err)
	}

	if _, err := New(t.Context(), storage, "test"); err == nil {
		t.Fatal("New accepted a world-readable TLS key")
	}
	info, err := os.Stat(store.KeyPath())
	if err != nil {
		t.Fatalf("stat key: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("key mode = %04o after rejection, want the evidence left untouched (0644)", got)
	}

	// A tighter certificate mode is not a fault.
	if err := os.Chmod(store.KeyPath(), 0o600); err != nil {
		t.Fatalf("restore key mode: %v", err)
	}
	if err := os.Chmod(store.CertPath(), 0o600); err != nil {
		t.Fatalf("tighten cert mode: %v", err)
	}
	if _, err := New(t.Context(), storage, "test"); err != nil {
		t.Fatalf("New rejected a 0600 certificate: %v", err)
	}
}
