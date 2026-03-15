package release

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ReleaseSource defines the interface for checking for updates.
type ReleaseSource interface {
	GetLatestVersion(ctx context.Context, releaseURL string) (string, error)
}

// GenericReleaseSource implements the ReleaseSource interface for generic platforms.
type GenericReleaseSource struct{}

func (g *GenericReleaseSource) GetLatestVersion(ctx context.Context, releaseURL string) (string, error) {
	return getLatestVersion(ctx, releaseURL)
}

func getLatestVersion(ctx context.Context, releaseURL string) (string, error) {
	// Construct the version URL by appending "version" to the release URL
	versionURL := strings.TrimSuffix(releaseURL, "/") + "/version"

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Create request with context
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, versionURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Execute request
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch version: %w", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	// Trim whitespace and return
	version := strings.TrimSpace(string(body))
	if version == "" {
		return "", fmt.Errorf("empty version response")
	}

	return version, nil
}
