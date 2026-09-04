// --- FILE update ---

package release

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGetLatestVersionValidatesResponse(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		want    string
		wantErr bool
	}{
		{name: "valid semantic version", status: http.StatusOK, body: "v1.2.3\n", want: "v1.2.3"},
		{name: "prerelease is still valid", status: http.StatusOK, body: "v2.0.0-rc.1", want: "v2.0.0-rc.1"},
		{name: "invalid semantic version", status: http.StatusOK, body: "latest", wantErr: true},
		{name: "missing v prefix", status: http.StatusOK, body: "1.2.3", wantErr: true},
		{name: "empty body", status: http.StatusOK, body: "  \n", wantErr: true},
		{name: "oversized response", status: http.StatusOK, body: strings.Repeat("x", maxVersionResponseBytes+1), wantErr: true},
		{name: "unexpected status", status: http.StatusBadGateway, body: "v1.2.3", wantErr: true},
		{name: "not found", status: http.StatusNotFound, body: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = fmt.Fprint(w, tt.body)
			}))
			defer server.Close()

			source := &GenericReleaseSource{}
			got, err := source.GetLatestVersion(context.Background(), server.URL)
			if (err != nil) != tt.wantErr {
				t.Fatalf("GetLatestVersion() error = %v, wantErr %t", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("GetLatestVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetLatestVersionRequestShape(t *testing.T) {
	const userAgent = "Mozilla/5.0 (compatible; sprout/1.2; +https://example.invalid)"
	var got *http.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		_, _ = fmt.Fprint(w, "v1.0.0")
	}))
	defer server.Close()

	source := &GenericReleaseSource{UserAgent: userAgent}
	for _, base := range []string{server.URL + "/releases", server.URL + "/releases/"} {
		if _, err := source.GetLatestVersion(context.Background(), base); err != nil {
			t.Fatalf("GetLatestVersion(%q): %v", base, err)
		}
		if got.URL.Path != "/releases/version" {
			t.Fatalf("path for base %q = %q, want /releases/version", base, got.URL.Path)
		}
		if ua := got.Header.Get("User-Agent"); ua != userAgent {
			t.Fatalf("User-Agent = %q, want %q", ua, userAgent)
		}
		if cc := got.Header.Get("Cache-Control"); cc != "no-cache" {
			t.Fatalf("Cache-Control = %q, want no-cache", cc)
		}
	}
}

func TestGetLatestVersionRejectsBadReleaseURLs(t *testing.T) {
	source := &GenericReleaseSource{}
	for _, raw := range []string{"", "file:///srv/release/", "ftp://host/", "https:///no-host", "://broken"} {
		_, err := source.GetLatestVersion(context.Background(), raw)
		if !errors.Is(err, ErrInvalidReleaseURL) {
			t.Fatalf("GetLatestVersion(%q) error = %v, want ErrInvalidReleaseURL", raw, err)
		}
	}
}

func TestGetLatestVersionHonorsContext(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	source := &GenericReleaseSource{}
	_, err := source.GetLatestVersion(ctx, server.URL)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
}
