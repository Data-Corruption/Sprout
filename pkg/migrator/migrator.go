package migrator

import (
	"fmt"

	"github.com/Data-Corruption/lmdb-go/lmdb"
	"github.com/Data-Corruption/stdx/xlog"
)

// Operation defines the actual database modification.
type Operation func(txn *lmdb.Txn) error

// Migration represents a single version step.
type Migration struct {
	ID   string    // e.g., "v1.0.0", "20231012_add_users"
	Desc string    // Human readable description for logs
	Up   Operation // The function to execute
}

// Migrator manages the execution of migrations.
type Migrator struct {
	steps []Migration
}

// New creates a Migrator instance with an empty migration list.
func New() *Migrator {
	return &Migrator{
		steps: make([]Migration, 0),
	}
}

// Add registers a new migration step.
// Order matters! Call this in the exact order you want migrations to run.
func (m *Migrator) Add(id string, desc string, op Operation) {
	m.steps = append(m.steps, Migration{
		ID:   id,
		Desc: desc,
		Up:   op,
	})
}

// Run executes all pending migrations based on the current version.
// It returns the new version string and any error encountered.
func (m *Migrator) Run(txn *lmdb.Txn, currentVersion string, logger *xlog.Logger) (string, error) {
	startIndex := 0

	// 1. Determine where to start
	if currentVersion != "" {
		found := false
		for i, step := range m.steps {
			if step.ID == currentVersion {
				startIndex = i + 1 // Start at the *next* step
				found = true
				break
			}
		}
		if !found {
			return currentVersion, fmt.Errorf("current version %q not found in migration history; database state is unknown", currentVersion)
		}
	}

	// 2. Apply pending migrations (skipped entirely if up-to-date)
	finalVersion := currentVersion
	for i := startIndex; i < len(m.steps); i++ {
		step := m.steps[i]

		logger.Infof("Applying migration: %s - %s", step.ID, step.Desc)
		if err := step.Up(txn); err != nil {
			return finalVersion, fmt.Errorf("failed to apply migration %q (%s): %w", step.ID, step.Desc, err)
		}

		finalVersion = step.ID
	}

	return finalVersion, nil
}
