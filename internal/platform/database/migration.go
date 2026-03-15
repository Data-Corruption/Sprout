package database

import (
	"errors"
	"fmt"
	"sprout/internal/types"
	"sprout/pkg/migrator"

	"github.com/Data-Corruption/lmdb-go/lmdb"
	"github.com/Data-Corruption/lmdb-go/wrap"
	"github.com/Data-Corruption/stdx/xlog"
)

func Migrate(db *wrap.DB, logger *xlog.Logger) error {
	m := migrator.New()

	// Add steps here. Order matters!

	m.Add("v1", "Initial Schema", func(txn *lmdb.Txn) error {
		// Create Config with default values
		cfg := types.DefaultConfig()

		// Store config (ConfigDBI is already cached at this point)
		if err := TxnPut(txn, *ConfigDBI, []byte(ConfigDataKey), &cfg, EncodingJSON); err != nil {
			return fmt.Errorf("failed to store initial config: %w", err)
		}

		return nil
	})

	/* Example version bump
	migrator.Add("v2", "Add Thing to Thing", func(txn *lmdb.Txn) error {
		// do v2 stuff
		return nil
	})
	*/

	return db.Update(func(txn *lmdb.Txn) error {
		// Get current version (ConfigDBI is already cached at this point)
		currentVer := ""
		if err := TxnGet(txn, *ConfigDBI, []byte(ConfigVersionKey), &currentVer, EncodingJSON); err != nil {
			if !errors.Is(err, ErrNotFound) {
				return fmt.Errorf("failed to get config version: %w", err)
			}
			currentVer = ""
		}

		// Run migrations
		newVer, err := m.Run(txn, currentVer, logger)
		if err != nil {
			return err
		}

		// Update version in DB
		if err := TxnPut(txn, *ConfigDBI, []byte(ConfigVersionKey), newVer, EncodingJSON); err != nil {
			return fmt.Errorf("failed to update config version: %w", err)
		}

		logger.Infof("Migrated from %q to %q\n", currentVer, newVer)
		return nil
	})
}
