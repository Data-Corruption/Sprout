//go:build !windows

package layout

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureRejectsUnsafeDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "storage")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("create unsafe root: %v", err)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatalf("chmod unsafe root: %v", err)
	}
	if err := FromStorage(root, "sprout").Ensure(); err == nil || !strings.Contains(err.Error(), "want 0700") {
		t.Fatalf("Ensure error = %v, want mode rejection", err)
	}
}

func TestEnsureRejectsSymlinkedDirectory(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatalf("create target: %v", err)
	}
	root := filepath.Join(t.TempDir(), "storage")
	if err := os.Symlink(target, root); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	if err := FromStorage(root, "sprout").Ensure(); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Ensure error = %v, want symlink rejection", err)
	}
}

func TestNewUsesSingleHomeRootAndIgnoresXDG(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("home resolution intentionally refuses a real root process")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	l, err := New("sprout", false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if want := filepath.Join(home, ".sprout"); l.Storage != want {
		t.Fatalf("storage = %q, want %q", l.Storage, want)
	}
	if !strings.HasPrefix(l.Control, l.Storage+string(filepath.Separator)) {
		t.Fatalf("control %q is outside storage %q", l.Control, l.Storage)
	}
}

func TestNewRejectsRelativeHome(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root is rejected before home resolution")
	}
	t.Setenv("HOME", "relative-home")
	if _, err := New("sprout", false); err == nil || !strings.Contains(err.Error(), "not absolute") {
		t.Fatalf("New error = %v, want relative-home rejection", err)
	}
}
