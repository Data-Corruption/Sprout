// --- FILE update ---

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const (
	// UpdateGuidance deliberately avoids naming an installer or release host.
	// Managed and mirrored installs may have an administrator-approved source.
	UpdateGuidance = "Repeat your original installation steps or follow your administrator's update instructions."
)

var (
	ErrUpdatesDisabled = errors.New("updates disabled: no release-url file (mirror install?)")
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
// Notification state, when retained, is persisted by the periodic checker.
// Development builds return [ErrDevBuild]; installs without a release-url
// return [ErrUpdatesDisabled].
func (a *App) CheckForUpdate(ctx context.Context) (bool, error) {
	a.updateCheckMu.Lock()
	defer a.updateCheckMu.Unlock()
	return a.checkForUpdate(ctx)
}

func (a *App) checkForUpdate(ctx context.Context) (bool, error) {
	if a.buildInfo.Version == "" {
		return false, fmt.Errorf("app version is not set")
	}
	if a.buildInfo.DevMode {
		return false, ErrDevBuild
	}

	checkCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	releaseURL, err := a.releaseURL()
	if err != nil {
		return false, err
	}
	latest, err := a.ReleaseSource.GetLatestVersion(checkCtx, releaseURL)
	if err != nil {
		return false, err
	}

	updateAvailable := semver.Compare(latest, a.buildInfo.Version) > 0
	a.Log.Debugf("Latest version: %s, Current version: %s, Update available: %t",
		latest, a.buildInfo.Version, updateAvailable)
	return updateAvailable, nil
}
