package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sprout/internal/build"
	"sprout/internal/platform/database"
	"sprout/internal/platform/database/config"
	"testing"

	"github.com/Data-Corruption/stdx/xlog"
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
	dbPath := filepath.Join(tmpDir, "db")
	logPath := filepath.Join(tmpDir, "logs")

	// Initialize Logger
	logger, err := xlog.New(logPath, "debug")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Close()

	// Initialize DB
	db, err := database.New(dbPath, logger) // ignoring stale readers count
	if err != nil {
		t.Fatalf("Failed to create db: %v", err)
	}
	defer db.Close()

	tests := []struct {
		name           string
		currentVersion string
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
			currentVersion: "vX.X.X",
			latestVersion:  "v9.9.9",
			wantUpdate:     false,
			wantError:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			releaseURL := "https://download.example-app.com/release/"
			if err := os.WriteFile(filepath.Join(tmpDir, releaseURLFileName), []byte(releaseURL), 0600); err != nil {
				t.Fatalf("Failed to write release URL file: %v", err)
			}

			// Setup App with Mock
			bi := build.Info()
			bi.Version = tt.currentVersion
			app := &App{
				DB:         db,
				Log:        logger,
				StorageDir: tmpDir,
				ReleaseSource: &MockReleaseSource{
					LatestVersion: tt.latestVersion,
					Error:         tt.mockError,
				},
				buildInfo: bi,
				Context:   context.Background(),
			}

			// Run CheckForUpdate
			gotUpdate, err := app.CheckForUpdate()

			// Check Error
			if (err != nil) != tt.wantError {
				t.Errorf("CheckForUpdate() error = %v, wantError %v", err, tt.wantError)
				return
			}

			// Check Result
			if gotUpdate != tt.wantUpdate {
				t.Errorf("CheckForUpdate() = %v, want %v", gotUpdate, tt.wantUpdate)
			}

			// Verify DB state if successful
			if !tt.wantError && tt.currentVersion != "vX.X.X" {
				cfg, err := config.View(db)
				if err != nil {
					t.Fatalf("Failed to view config: %v", err)
				}
				if cfg.UpdateAvailable != tt.wantUpdate {
					t.Errorf("DB Config UpdateAvailable = %v, want %v", cfg.UpdateAvailable, tt.wantUpdate)
				}
			}
		})
	}
}
