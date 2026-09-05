// --- FILE update ---

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"sprout/internal/platform/database/config"
	"sprout/internal/types"

	"golang.org/x/mod/semver"
)

const (
	// UpdateGuidance deliberately avoids naming an installer or release host.
	// Managed and mirrored installs may have an administrator-approved source.
	UpdateGuidance = "Repeat your original installation steps or follow your administrator's update instructions."
)

var (
	ErrUpdatesDisabled = errors.New("updates disabled: no release-url file")
)

func normalizeReleaseURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.TrimRight(trimmed, "/")
	if trimmed == "" {
		return "", fmt.Errorf("release URL is empty")
	}
	return trimmed + "/", nil
}

func loadReleaseURL(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("release URL path is not set")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: missing %s", ErrUpdatesDisabled, path)
		}
		return "", fmt.Errorf("read release URL file: %w", err)
	}

	url, err := normalizeReleaseURL(string(data))
	if err != nil {
		return "", fmt.Errorf("invalid release URL in %s: %w", path, err)
	}
	return url, nil
}

func (a *App) releaseURL() (string, error) {
	return loadReleaseURL(a.Layout.ReleaseURL)
}

// CheckForUpdate performs a fresh check against the configured release source.
// The source, version, and check time are persisted for notices and the service.
// Development builds return [ErrDevBuild]; installs without a release-url
// return [ErrUpdatesDisabled].
func (a *App) CheckForUpdate(ctx context.Context) (bool, error) {
	a.updateCheckMu.Lock()
	defer a.updateCheckMu.Unlock()
	result, err := a.checkForUpdate(ctx)
	if err != nil {
		return false, err
	}
	if _, err := config.Update(a.DB, func(cfg *types.Configuration) error {
		setUpdateCheckResult(cfg, result, time.Now())
		return nil
	}); err != nil {
		return false, fmt.Errorf("persist update check: %w", err)
	}
	return result.available(a.buildInfo.Version), nil
}

type updateResult struct{ source, version string }

func (r updateResult) available(current string) bool {
	return semver.IsValid(r.version) && semver.Compare(r.version, current) > 0
}

// UpdateAvailable derives availability only from the installation's current
// source. A source change or successful upgrade cannot leave a stale notice.
func (a *App) UpdateAvailable(cfg *types.Configuration) bool {
	source, err := a.releaseURL()
	return err == nil && !a.buildInfo.DevMode && source == cfg.UpdateCheckSource &&
		(updateResult{version: cfg.LatestUpdateVersion}).available(a.buildInfo.Version)
}

func (a *App) checkForUpdate(ctx context.Context) (updateResult, error) {
	if a.buildInfo.Version == "" {
		return updateResult{}, fmt.Errorf("app version is not set")
	}
	if a.buildInfo.DevMode {
		return updateResult{}, ErrDevBuild
	}

	checkCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	releaseURL, err := a.releaseURL()
	if err != nil {
		return updateResult{}, err
	}
	latest, err := a.ReleaseSource.GetLatestVersion(checkCtx, releaseURL)
	if err != nil {
		return updateResult{}, err
	}

	updateAvailable := semver.Compare(latest, a.buildInfo.Version) > 0
	a.Log.Debugf("Latest version: %s, Current version: %s, Update available: %t",
		latest, a.buildInfo.Version, updateAvailable)
	return updateResult{source: releaseURL, version: latest}, nil
}
