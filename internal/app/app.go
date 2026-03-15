// Package app implements the application, following the dependency injection pattern.
package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sprout/internal/build"
	"sprout/internal/platform/database"
	"sprout/internal/platform/database/config"
	"sprout/internal/platform/release"
	"sprout/internal/types"
	"sprout/internal/ui"
	"sprout/pkg/x"
	"strings"
	"sync"
	"time"

	"github.com/Data-Corruption/lmdb-go/wrap"
	"github.com/Data-Corruption/stdx/xhttp"
	"github.com/Data-Corruption/stdx/xlog"
	"github.com/urfave/cli/v3"
	"golang.org/x/mod/semver"
)

type CleanupFunc func() error

/*
App represents the application, following the dependency injection pattern.

It provides:
  - build-time variables
  - injected services
  - lifecycle management
  - update handlers and migration synchronization
*/
type App struct {
	// injected services, etc.

	DB            *wrap.DB
	Log           *xlog.Logger
	Server        *xhttp.Server
	UI            *ui.UI
	BaseURL       string // e.g., "https://example.com"
	UserAgent     string // e.g., "Mozilla/5.0 (compatible; <Name>/1.2.3; +<ContactURL>)"
	StorageDir    string // (e.g., ~/.<Name>)
	RuntimeDir    string // (e.g., XDG_RUNTIME_DIR/<Name>, fallback to /tmp/<Name>-USER)
	TempDir       string // (e.g., StorageDir/tmp)
	ReleaseSource release.ReleaseSource
	buildInfo     build.BuildInfo // read-only

	// lifecycle management

	cleanup       []CleanupFunc
	cleanupOnce   sync.Once
	postCleanup   CleanupFunc
	postCleanupMu sync.Mutex
	uOnce         sync.Once // prep update only once before exiting
	// Inside commands, you can use <-a.Context.Done() to check for cancellation.
	// You don't need to do this for the example service, the http server
	// wrapper has its own signal listener.
	Context context.Context
}

func New(buildInfo build.BuildInfo) *App {
	return &App{
		buildInfo: buildInfo,
	}
}

func (a *App) BuildInfo() build.BuildInfo {
	return a.buildInfo
}

func (a *App) Init(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	// paths
	var err error
	if a.StorageDir, err = getStoragePath(a.buildInfo.Name); err != nil {
		return nil, err
	}
	if a.RuntimeDir, err = getRuntimePath(a.buildInfo.Name); err != nil {
		return nil, err
	}
	a.TempDir = filepath.Join(a.StorageDir, "tmp")
	if err := os.MkdirAll(a.TempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}

	// migration guard before touching anything
	if !cmd.Bool("migrate") {
		if err := a.mguard(); err != nil {
			return ctx, fmt.Errorf("failed to setup migration guard: %w", err)
		}
	} else {
		// migrate flag set, we are the migrator instance, proceed without guard
		fmt.Printf("%s version %s\n", a.buildInfo.Name, a.buildInfo.Version)
	}

	// logger
	logOverride := cmd.String("log") != ""
	a.Log, err = xlog.New(filepath.Join(a.StorageDir, "logs"), x.Ternary(logOverride, cmd.String("log"), "none"))
	if err != nil {
		return ctx, fmt.Errorf("failed to initialize logger: %w", err)
	}
	a.AddCleanup(a.Log.Close)

	a.Log.Debugf("Starting %s, version: %s, storage path: %s, runtime path: %s",
		a.buildInfo.Name, a.buildInfo.Version, a.StorageDir, a.RuntimeDir)

	// database
	if a.DB, err = database.New(filepath.Join(a.StorageDir, "db"), a.Log); err != nil {
		return ctx, fmt.Errorf("failed to initialize database: %w", err)
	}
	a.AddCleanup(func() error {
		// store PreUpdateVersion on shutdown, unless we are the migrator instance
		if !cmd.Bool("migrate") {
			if _, err := config.Update(a.DB, func(cfg *types.Configuration) error {
				cfg.PreUpdateVersion = a.buildInfo.Version
				return nil
			}); err != nil {
				a.Log.Errorf("failed to set PreUpdateVersion on shutdown: %v", err)
			}
		}
		a.DB.Close()
		return nil
	})
	a.Log.Debug("Database initialized")

	// get config
	cfg, err := config.View(a.DB)
	if err != nil {
		return ctx, fmt.Errorf("failed to view config: %w", err)
	}

	// override port (useful for testing)
	oPort := cmd.Int("port")
	if oPort != 0 {
		cfg.Port = oPort
	}

	// calculate BaseURL
	if a.BaseURL, err = getBaseURL(cfg); err != nil {
		return ctx, fmt.Errorf("failed to get base URL: %w", err)
	}
	a.Log.Debugf("Base URL: %s", a.BaseURL)

	// set UserAgent
	mmVer := strings.TrimPrefix(semver.MajorMinor(a.buildInfo.Version), "v")
	a.UserAgent = fmt.Sprintf("Mozilla/5.0 (compatible; %s/%s; +%s)", a.buildInfo.Name, mmVer, a.buildInfo.ContactURL)

	// set log level
	if !logOverride {
		if err := a.Log.SetLevel(cfg.LogLevel); err != nil {
			return ctx, fmt.Errorf("failed to set log level: %w", err)
		}
	}
	// put logger into context
	ctx = xlog.IntoContext(ctx, a.Log)

	// store context for use in update checking, etc.
	a.Context = ctx

	// load frontend
	if a.UI, err = ui.New(); err != nil {
		return ctx, fmt.Errorf("failed to load UI: %w", err)
	}

	// initialize release source for update checking
	a.ReleaseSource = &release.GenericReleaseSource{}

	// update checking
	if err := a.startAutoChecker(cfg); err != nil {
		return ctx, fmt.Errorf("failed to start auto checker: %w", err)
	}

	return ctx, nil
}

func (a *App) Close() {
	a.cleanupOnce.Do(func() {
		// call cleanup funcs in reverse order
		for i := len(a.cleanup) - 1; i >= 0; i-- {
			if err := a.cleanup[i](); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to clean up: %v\n", err)
			}
		}
		// call post cleanup func if set
		a.postCleanupMu.Lock()
		defer a.postCleanupMu.Unlock()
		if a.postCleanup != nil {
			time.Sleep(500 * time.Millisecond) // not sure if i need this actually
			if err := a.postCleanup(); err != nil {
				fmt.Fprintf(os.Stderr, "Post cleanup failure: %v\n", err)
			}
		}
	})
}

func (a *App) AddCleanup(f func() error) {
	a.cleanup = append(a.cleanup, f)
}

var ErrPostCleanupSet = errors.New("post cleanup already set")

// SetPostCleanup sets the post cleanup func. It returns an error if it's already set.
func (a *App) SetPostCleanup(f func() error) error {
	a.postCleanupMu.Lock()
	defer a.postCleanupMu.Unlock()

	if a.postCleanup != nil {
		return ErrPostCleanupSet
	}

	a.postCleanup = f
	return nil
}

// getStoragePath calculates the storage path for the application (~/.appName).
func getStoragePath(appName string) (string, error) {
	// get home dir
	home, err := x.GetUserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "."+appName), nil
}

// getRuntimePath calculates the runtime path for the application.
// Prefers XDG_RUNTIME_DIR, falls back to /tmp/appName-USER.
func getRuntimePath(appName string) (string, error) {
	// prefer XDG_RUNTIME_DIR (typically /run/user/UID)
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		return filepath.Join(runtimeDir, appName), nil
	}

	// fallback for non-systemd systems
	// include username to avoid conflicts in shared /tmp
	username := os.Getenv("USER")
	if username == "" {
		u, err := user.Current()
		if err != nil {
			return "", fmt.Errorf("cannot determine current user: %w", err)
		}
		username = u.Username
	}

	return filepath.Join("/tmp", appName+"-"+username), nil
}

func getBaseURL(cfg *types.Configuration) (string, error) {
	port := cfg.Port
	host := cfg.Host
	proxyPort := cfg.ProxyPort

	// calculate that shit
	host = x.Ternary(host != "", host, "localhost")
	port = x.Ternary(proxyPort != 0, proxyPort, port)
	hidePort := port == 80 || port == 443
	scheme := x.Ternary(port == 443, "https", "http")
	baseURL := fmt.Sprintf("%s://%s%s", scheme, host, x.Ternary(hidePort, "", fmt.Sprintf(":%d", port)))
	return baseURL, nil
}
