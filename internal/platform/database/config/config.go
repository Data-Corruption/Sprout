package config

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sprout/internal/platform/database"
	"sprout/internal/types"
	"sprout/pkg/xlog"
	"strings"
)

var ErrDatabaseClosed = errors.New("database is closed")

// View retrieves a copy of the current configuration from the database.
func View(db *sql.DB) (*types.Configuration, error) {
	if db == nil {
		return nil, ErrDatabaseClosed
	}
	var data string
	if err := db.QueryRow(
		`SELECT value FROM config WHERE key = ?`, database.ConfigDataKey,
	).Scan(&data); err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}
	var cfg types.Configuration
	if err := json.Unmarshal([]byte(data), &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}
	return &cfg, nil
}

// Update updates the configuration in the database using the provided update
// function. The read-modify-write runs in a single immediate write
// transaction, so concurrent updates (including from other processes)
// serialize cleanly. If updateFunc returns an error, nothing is persisted.
func Update(db *sql.DB, updateFunc func(cfg *types.Configuration) error) (*types.Configuration, error) {
	if db == nil {
		return nil, ErrDatabaseClosed
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to begin config update: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit

	cfg, err := UpdateTx(tx, updateFunc)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit config update: %w", err)
	}
	return cfg, nil
}

// UpdateTx updates the configuration within an existing transaction. The
// caller owns the transaction and must commit or roll it back. This lets
// config changes remain atomic with related database writes.
func UpdateTx(tx *sql.Tx, updateFunc func(cfg *types.Configuration) error) (*types.Configuration, error) {
	if tx == nil {
		return nil, ErrDatabaseClosed
	}

	var data string
	if err := tx.QueryRow(
		`SELECT value FROM config WHERE key = ?`, database.ConfigDataKey,
	).Scan(&data); err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}
	var cfg types.Configuration
	if err := json.Unmarshal([]byte(data), &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	if err := updateFunc(&cfg); err != nil {
		return nil, fmt.Errorf("update function failed: %w", err)
	}

	normalize(&cfg)
	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	newData, err := json.Marshal(&cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config: %w", err)
	}
	if _, err := tx.Exec(
		`UPDATE config SET value = ? WHERE key = ?`, string(newData), database.ConfigDataKey,
	); err != nil {
		return nil, fmt.Errorf("failed to write config: %w", err)
	}
	return &cfg, nil
}

func normalize(cfg *types.Configuration) {
	cfg.LogLevel = strings.TrimSpace(cfg.LogLevel)
	if level, err := xlog.NormalizeLevel(cfg.LogLevel); err == nil {
		cfg.LogLevel = level
	}
	// --- BEGIN service.https ---
	cfg.UIBind = strings.TrimSpace(cfg.UIBind)
	cfg.ProxyBind = strings.TrimSpace(cfg.ProxyBind)
	for i := range cfg.Credentials {
		cfg.Credentials[i].Username = types.NormalizeUsername(cfg.Credentials[i].Username)
	}
	// --- END service.https ---
}
