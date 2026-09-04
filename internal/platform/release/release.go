// --- FILE update ---

// Package release resolves the newest published version from a release host.
//
// A release host publishes one root `version` object containing a single
// SemVer string. Installers read that pointer and pin it for the rest of their
// run; this package reads the same object so the application and the
// installer can never disagree about what "latest" means. Everything else
// about a release (checksums, signatures, binaries) lives under the immutable
// `releases/<version>/` prefix and is the installer's concern, not ours.
package release

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/mod/semver"
)

// maxVersionResponseBytes bounds the pointer read. A valid SemVer string is a
// few bytes; anything approaching this limit is a misconfigured or hostile host.
const maxVersionResponseBytes = 64

// ErrInvalidReleaseURL reports a release URL the source refuses to contact.
var ErrInvalidReleaseURL = errors.New("invalid release URL")

// ReleaseSource answers "what is the newest published version?" for a release
// URL. The App holds one so tests can substitute a fake without a network.
type ReleaseSource interface {
	GetLatestVersion(ctx context.Context, releaseURL string) (string, error)
}

// GenericReleaseSource reads the root version pointer over HTTP or HTTPS.
//
// It has no timeout of its own. Callers bound each check with their context,
// and a second, hidden deadline here would only make the effective limit
// whichever one happens to be shorter.
type GenericReleaseSource struct {
	// UserAgent identifies this application to the release host. The App
	// builds one from its name, version, and contact URL so a host operator
	// can tell installations apart from browsers and bots. Empty falls back to
	// Go's default.
	UserAgent string

	// Client is optional. The zero value uses http.DefaultTransport with no
	// client-level timeout, which is what every caller in this project wants.
	Client *http.Client
}

// GetLatestVersion fetches `<releaseURL>/version` and returns its trimmed,
// validated SemVer content.
func (s *GenericReleaseSource) GetLatestVersion(ctx context.Context, releaseURL string) (string, error) {
	target, err := versionURL(releaseURL)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", fmt.Errorf("build version request: %w", err)
	}
	if s.UserAgent != "" {
		req.Header.Set("User-Agent", s.UserAgent)
	}
	req.Header.Set("Accept", "text/plain")
	// The pointer is the one object on the host that moves. Ask intermediaries
	// not to hand back a stale copy that predates a promotion.
	req.Header.Set("Cache-Control", "no-cache")

	client := s.Client
	if client == nil {
		client = &http.Client{}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", target, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch %s: unexpected status %s", target, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxVersionResponseBytes+1))
	if err != nil {
		return "", fmt.Errorf("read version pointer: %w", err)
	}
	if len(body) > maxVersionResponseBytes {
		return "", fmt.Errorf("version pointer exceeds %d bytes", maxVersionResponseBytes)
	}

	version := strings.TrimSpace(string(body))
	switch {
	case version == "":
		return "", fmt.Errorf("version pointer is empty")
	case !semver.IsValid(version):
		return "", fmt.Errorf("version pointer %q is not a valid semantic version", version)
	}
	return version, nil
}

// versionURL joins the root pointer name onto a validated release URL.
// Only http and https are meaningful for a release host; a file or custom
// scheme reaching this point is a configuration error worth surfacing rather
// than something to try anyway.
func versionURL(releaseURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(releaseURL))
	if err != nil {
		return "", fmt.Errorf("%w %q: %w", ErrInvalidReleaseURL, releaseURL, err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", fmt.Errorf("%w %q: scheme must be http or https", ErrInvalidReleaseURL, releaseURL)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("%w %q: missing host", ErrInvalidReleaseURL, releaseURL)
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/") + "/version"
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}
