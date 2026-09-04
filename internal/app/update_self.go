// --- FILE update.self ---

package app

import (
	"fmt"

	"sprout/internal/platform/database/config"
	"sprout/internal/types"
)

func (a *App) setUpdateAvailable(available bool) error {
	// --- BEGIN update.notifications ---
	if _, err := config.Update(a.DB, func(cfg *types.Configuration) error {
		cfg.UpdateAvailable = available
		return nil
	}); err != nil {
		return fmt.Errorf("set available-update flag to %t: %w", available, err)
	}
	// --- END update.notifications ---

	return nil
}
