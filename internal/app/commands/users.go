// --- FILE service.https ---

package commands

import (
	"context"
	"database/sql"
	"fmt"
	"sprout/internal/app"
	"sprout/internal/platform/database/config"
	"sprout/internal/platform/database/sessions"
	"sprout/internal/types"
	"sprout/pkg/crypto"
	"sprout/pkg/xterm/prompt"
	"strings"

	"github.com/urfave/cli/v3"
)

func usersCommand(a *app.App) *cli.Command {
	return &cli.Command{
		Name:  "users",
		Usage: "manage dashboard users",
		Commands: []*cli.Command{
			{
				Name:  "add",
				Usage: "add a dashboard user",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "username",
						Usage:    "unique username",
						Required: true,
					},
					&cli.StringFlag{
						Name:  "perms",
						Usage: `space-separated permissions (e.g. "admin", "admin !server.control", "settings")`,
						Value: "admin",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					username := types.NormalizeUsername(cmd.String("username"))
					permStr := cmd.String("perms")

					if username == "" {
						return fmt.Errorf("username cannot be empty")
					}
					plain, err := prompt.Secret("Password")
					if err != nil {
						return err
					}
					if plain == "" {
						return fmt.Errorf("password cannot be empty")
					}

					perms, err := types.ParsePerms(strings.Fields(permStr))
					if err != nil {
						return fmt.Errorf("invalid perms: %w", err)
					}

					passHash, passSalt, err := crypto.HashPassword(plain)
					if err != nil {
						return fmt.Errorf("failed to hash password: %w", err)
					}

					if _, err := config.Update(a.DB, func(cfg *types.Configuration) error {
						for _, c := range cfg.Credentials {
							if c.Username == username {
								return fmt.Errorf("credential with username %q already exists", username)
							}
						}
						cfg.Credentials = append(cfg.Credentials, types.Credential{
							Username: username,
							PassHash: passHash,
							PassSalt: passSalt,
							Perms:    perms,
						})
						return nil
					}); err != nil {
						return fmt.Errorf("failed to add credential: %w", err)
					}

					fmt.Printf("Added credential %q with perms: %s\n", username, perms)
					return nil
				},
			},
			{
				Name:  "list",
				Usage: "list dashboard users",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cfg, err := config.View(a.DB)
					if err != nil {
						return fmt.Errorf("failed to read config: %w", err)
					}
					if len(cfg.Credentials) == 0 {
						fmt.Println("No credentials configured.")
						return nil
					}
					for _, c := range cfg.Credentials {
						fmt.Printf("  %s  [%s]\n", c.Username, c.Perms)
					}
					return nil
				},
			},
			{
				Name:  "remove",
				Usage: "remove a dashboard user",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "username",
						Usage:    "username of the credential to remove",
						Required: true,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					username := types.NormalizeUsername(cmd.String("username"))
					n, err := removeCredential(ctx, a.DB, username)
					if err != nil {
						return err
					}

					fmt.Printf("Removed credential %q", username)
					if n > 0 {
						fmt.Printf(" and revoked %d active session(s)", n)
					}
					fmt.Println()
					return nil
				},
			},
		},
	}
}

func removeCredential(ctx context.Context, db *sql.DB, username string) (int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to begin credential removal: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit

	if _, err := config.UpdateTx(tx, func(cfg *types.Configuration) error {
		for i, credential := range cfg.Credentials {
			if credential.Username == username {
				cfg.Credentials = append(cfg.Credentials[:i], cfg.Credentials[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("credential %q not found", username)
	}); err != nil {
		return 0, fmt.Errorf("failed to remove credential: %w", err)
	}

	revoked, err := sessions.DeleteByUsernameTx(tx, username)
	if err != nil {
		return 0, fmt.Errorf("failed to revoke credential sessions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit credential removal: %w", err)
	}
	return revoked, nil
}
