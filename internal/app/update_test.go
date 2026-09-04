// --- FILE update ---

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sprout/internal/build"
	"sprout/internal/layout"
	"sprout/internal/platform/database"
	"testing"

	"sprout/pkg/xlog"
)

// MockReleaseSource is a mock implementation of ReleaseSource for testing.
type MockReleaseSource struct {
	LatestVersion string
	Error         error
}

func (m *MockReleaseSource) GetLatestVersion(ctx context.Context, releaseURL string) (string, error) {
	return m.LatestVersion, m.Error
}

func TestCheckForUpdate(t *testing.T) {
	// Setup temporary directory for DB and Logs
	tmpDir := t.TempDir()
	if err := os.Chmod(tmpDir, 0o700); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(tmpDir, "db")
	logPath := filepath.Join(tmpDir, "logs")

	// Initialize Logger
	logger, err := xlog.New(logPath, "debug")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Close()

	// Initialize DB
	db, err := database.New(dbPath, logger, build.Info(), database.ApplyPendingMigrations)
	if err != nil {
		t.Fatalf("Failed to create db: %v", err)
	}
	defer db.Close()

	tests := []struct {
		name           string
		currentVersion string
		devMode        bool
		latestVersion  string
		mockError      error
		wantUpdate     bool
		wantError      bool
	}{
		{
			name:           "Update Available",
			currentVersion: "v1.0.0",
			latestVersion:  "v1.1.0",
			wantUpdate:     true,
			wantError:      false,
		},
		{
			name:           "No Update Available",
			currentVersion: "v1.1.0",
			latestVersion:  "v1.1.0",
			wantUpdate:     false,
			wantError:      false,
		},
		{
			name:           "Current Newer Than Latest (Dev)",
			currentVersion: "v1.2.0",
			latestVersion:  "v1.1.0",
			wantUpdate:     false,
			wantError:      false,
		},
		{
			name:           "Network Error",
			currentVersion: "v1.0.0",
			latestVersion:  "",
			mockError:      fmt.Errorf("network error"),
			wantUpdate:     false,
			wantError:      true,
		},
		{
			name:           "Dev Build Skipped",
			currentVersion: "v0.0.0-dev",
			devMode:        true,
			latestVersion:  "v9.9.9",
			wantUpdate:     false,
			wantError:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appLayout := layout.FromStorage(tmpDir, "sprout")
			if err := appLayout.Ensure(); err != nil {
				t.Fatal(err)
			}
			releaseURL := "https://download.example-app.com/release/"
			if err := os.WriteFile(appLayout.ReleaseURL, []byte(releaseURL), 0600); err != nil {
				t.Fatalf("Failed to write release URL file: %v", err)
			}

			// Setup App with Mock
			bi := build.Info()
			bi.Version = tt.currentVersion
			bi.DevMode = tt.devMode
			app := &App{
				DB:     db,
				Log:    logger,
				Layout: appLayout,
				ReleaseSource: &MockReleaseSource{
					LatestVersion: tt.latestVersion,
					Error:         tt.mockError,
				},
				buildInfo: bi,
				Context:   context.Background(),
			}

			// Run CheckForUpdate
			gotUpdate, err := app.CheckForUpdate(context.Background())

			// Check Error
			if (err != nil) != tt.wantError {
				t.Errorf("CheckForUpdate() error = %v, wantError %v", err, tt.wantError)
				return
			}

			// Check Result
			if gotUpdate != tt.wantUpdate {
				t.Errorf("CheckForUpdate() = %v, want %v", gotUpdate, tt.wantUpdate)
			}

		})
	}
}
