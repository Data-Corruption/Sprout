// Package layout defines every path owned by an installation.
package layout

import (
	"fmt"
	"path/filepath"
)

const (
	StateFileName          = "state.json"
	OperationLockFileName  = "operation.lock"
	LifecycleLockFileName  = "lifecycle.lock"
	ServiceLockFileName    = "service.lock"
	ServiceStopFileName    = "service.stop"
	ReleaseURLFileName     = "release-url"
	MaintenanceLogFileName = "maintenance.log"
)

// Layout is the canonical on-disk layout for one installation.
//
// Every field is absolute when Layout was returned by New. FromStorage is
// also exposed for tests and tools that have already resolved the storage
// root; callers of it are responsible for passing an absolute path.
type Layout struct {
	Storage string

	Data    string
	DB      string
	Secrets string
	Temp    string
	Env     string

	Control       string
	State         string
	OperationLock string
	LifecycleLock string
	Instances     string
	ServiceLock   string
	ServiceStop   string

	Maintenance     string
	CachedInstaller string
	CachedBundle    string
	ReleaseURL      string
	Jobs            string

	Logs           string
	MaintenanceLog string
}

// New resolves the per-user storage root and returns its canonical layout.
// Development builds receive a separate root, but retain the application
// name for files such as <app>.env.
func New(appName string, dev bool) (Layout, error) {
	if err := validateAppName(appName); err != nil {
		return Layout{}, err
	}
	pathName := appName
	if dev {
		pathName += "-dev"
	}
	storage, err := storageRoot(pathName)
	if err != nil {
		return Layout{}, err
	}
	return FromStorage(storage, appName), nil
}

// FromStorage derives every installation path from an already-resolved root.
// It does not touch the filesystem.
func FromStorage(storage, appName string) Layout {
	data := filepath.Join(storage, "data")
	control := filepath.Join(storage, "control")
	maintenance := filepath.Join(storage, "maintenance")
	logs := filepath.Join(storage, "logs")
	installer := installerFileName()
	return Layout{
		Storage: storage,
		Data:    data,
		DB:      filepath.Join(data, "db"),
		Secrets: filepath.Join(data, "secrets"),
		Temp:    filepath.Join(data, "tmp"),
		Env:     filepath.Join(data, appName+".env"),

		Control:       control,
		State:         filepath.Join(control, StateFileName),
		OperationLock: filepath.Join(control, OperationLockFileName),
		LifecycleLock: filepath.Join(control, LifecycleLockFileName),
		Instances:     filepath.Join(control, "instances"),
		ServiceLock:   filepath.Join(control, ServiceLockFileName),
		ServiceStop:   filepath.Join(control, ServiceStopFileName),

		Maintenance:     maintenance,
		CachedInstaller: filepath.Join(maintenance, installer),
		CachedBundle:    filepath.Join(maintenance, installer+".cosign.bundle"),
		ReleaseURL:      filepath.Join(maintenance, ReleaseURLFileName),
		Jobs:            filepath.Join(maintenance, "jobs"),

		Logs:           logs,
		MaintenanceLog: filepath.Join(logs, MaintenanceLogFileName),
	}
}

// Ensure creates and validates all application-owned directories. Callers
// participating in the lifecycle protocol should use EnsureRetained before
// taking a lifecycle lease and EnsureData after taking it.
func (l Layout) Ensure() error {
	if err := l.EnsureRetained(); err != nil {
		return err
	}
	return l.EnsureData()
}

// EnsureRetained prepares directories that survive uninstall. It is safe to
// call before taking a lifecycle lease because uninstall never removes them.
func (l Layout) EnsureRetained() error {
	for _, dir := range []string{
		l.Storage,
		l.Control,
		l.Instances,
		l.Maintenance,
		l.Jobs,
		l.Logs,
	} {
		if err := ensurePrivateDir(dir); err != nil {
			return fmt.Errorf("prepare private directory %q: %w", dir, err)
		}
	}
	return nil
}

// EnsureData prepares the directories removed by uninstall. Normal
// applications must hold a shared lifecycle lease while calling it so they
// cannot recreate data during an uninstall transaction.
func (l Layout) EnsureData() error {
	for _, dir := range []string{l.Data, l.DB, l.Secrets, l.Temp} {
		if err := ensurePrivateDir(dir); err != nil {
			return fmt.Errorf("prepare private directory %q: %w", dir, err)
		}
	}
	return nil
}

func validateAppName(name string) error {
	if name == "" {
		return fmt.Errorf("application name is empty")
	}
	for i := range len(name) {
		c := name[i]
		allowed := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
			c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.'
		if !allowed || i == 0 && (c == '-' || c == '.') {
			return fmt.Errorf("application name %q must match [A-Za-z0-9_][A-Za-z0-9._-]*", name)
		}
	}
	return nil
}
