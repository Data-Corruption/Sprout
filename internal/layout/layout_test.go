package layout

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFromStorageDefinesCanonicalLayout(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	got := FromStorage(root, "sprout")
	want := map[string]string{
		"data":            filepath.Join(root, "data"),
		"db":              filepath.Join(root, "data", "db"),
		"secrets":         filepath.Join(root, "data", "secrets"),
		"temp":            filepath.Join(root, "data", "tmp"),
		"env":             filepath.Join(root, "data", "sprout.env"),
		"control":         filepath.Join(root, "control"),
		"state":           filepath.Join(root, "control", "state.json"),
		"operation lock":  filepath.Join(root, "control", "operation.lock"),
		"lifecycle lock":  filepath.Join(root, "control", "lifecycle.lock"),
		"instances":       filepath.Join(root, "control", "instances"),
		"service lock":    filepath.Join(root, "control", "service.lock"),
		"service stop":    filepath.Join(root, "control", "service.stop"),
		"maintenance":     filepath.Join(root, "maintenance"),
		"release URL":     filepath.Join(root, "maintenance", "release-url"),
		"jobs":            filepath.Join(root, "maintenance", "jobs"),
		"logs":            filepath.Join(root, "logs"),
		"maintenance log": filepath.Join(root, "logs", "maintenance.log"),
	}
	fields := map[string]string{
		"data": got.Data, "db": got.DB, "secrets": got.Secrets,
		"temp": got.Temp, "env": got.Env, "control": got.Control,
		"state": got.State, "operation lock": got.OperationLock,
		"lifecycle lock": got.LifecycleLock, "instances": got.Instances,
		"service lock": got.ServiceLock, "service stop": got.ServiceStop,
		"maintenance": got.Maintenance, "release URL": got.ReleaseURL,
		"jobs": got.Jobs, "logs": got.Logs, "maintenance log": got.MaintenanceLog,
	}
	for name, path := range want {
		if fields[name] != path {
			t.Errorf("%s = %q, want %q", name, fields[name], path)
		}
	}
	if filepath.Dir(got.CachedInstaller) != got.Maintenance {
		t.Errorf("cached installer %q is not under maintenance", got.CachedInstaller)
	}
	if got.CachedBundle != got.CachedInstaller+".cosign.bundle" {
		t.Errorf("cached bundle = %q, want installer bundle", got.CachedBundle)
	}
}

func TestEnsureCreatesCanonicalPrivateDirectories(t *testing.T) {
	root := filepath.Join(t.TempDir(), "storage")
	l := FromStorage(root, "sprout")
	if err := l.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if err := l.Ensure(); err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	for _, dir := range []string{
		l.Storage, l.Data, l.DB, l.Secrets, l.Temp, l.Control,
		l.Instances, l.Maintenance, l.Jobs, l.Logs,
	} {
		info, err := filepath.Glob(dir)
		if err != nil || len(info) != 1 {
			t.Errorf("directory %q was not created: %v", dir, err)
		}
	}
}

func TestRetainedPreparationDoesNotRecreateUninstalledData(t *testing.T) {
	root := filepath.Join(t.TempDir(), "storage")
	l := FromStorage(root, "sprout")
	if err := l.EnsureRetained(); err != nil {
		t.Fatalf("EnsureRetained: %v", err)
	}
	for _, dir := range []string{l.Storage, l.Control, l.Instances, l.Maintenance, l.Jobs, l.Logs} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Errorf("retained directory %q was not created: %v", dir, err)
		}
	}
	if _, err := os.Stat(l.Data); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("data directory exists before lifecycle lease: %v", err)
	}
	if err := l.EnsureData(); err != nil {
		t.Fatalf("EnsureData: %v", err)
	}
	if info, err := os.Stat(l.Temp); err != nil || !info.IsDir() {
		t.Fatalf("mutable directories were not created: %v", err)
	}
}

func TestNewRejectsPathLikeAppNames(t *testing.T) {
	for _, name := range []string{"", ".", "..", "-app", "a/b", `a\\b`, "two words", "éclair"} {
		if _, err := New(name, false); err == nil {
			t.Errorf("New(%q) succeeded", name)
		}
	}
}
