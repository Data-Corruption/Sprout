package types

import (
	"fmt"
	"sprout/internal/build"
	"strings"
	"time"
)

type Configuration struct {
	LogLevel string `json:"logLevel"`
	// --- BEGIN service.https ---
	// UIBind is the self-signed HTTPS dashboard listener
	// (e.g. "127.0.0.1:8484", or ":8484" for explicit LAN exposure).
	UIBind string `json:"uiBind"`
	// ProxyBind is the optional loopback-only plain HTTP listener for local
	// reverse proxies such as Caddy (e.g. "127.0.0.1:8485"). Empty = disabled.
	ProxyBind string `json:"proxyBind"`
	// --- END service.https ---

	// --- BEGIN update ---
	UpdateNotifications    bool      `json:"updateNotifications"`
	LastUpdateCheck        time.Time `json:"lastUpdateCheck"`
	BackgroundUpdateChecks bool      `json:"backgroundUpdateChecks"`
	UpdateCheckSource      string    `json:"updateCheckSource"`
	LatestUpdateVersion    string    `json:"latestUpdateVersion"`
	// --- END update ---

	// --- BEGIN update.apply.auto ---
	AutomaticUpdates bool `json:"automaticUpdates"`
	// --- END update.apply.auto ---

	// --- BEGIN update.apply ---
	// LastShutdownVersion is compared with the running version after a restart
	// to infer whether an update occurred.
	LastShutdownVersion string `json:"lastShutdownVersion"`
	// --- END update.apply ---

	// --- BEGIN service.https ---
	// Dashboard auth (sessions live in the sessions table, not in config).
	Credentials []Credential `json:"credentials"`
	// --- END service.https ---
	// --- BEGIN service ---
	// Incremented whenever all retained service components report ready.
	StartCounter int `json:"startCounter"`
	// --- END service ---
}

// --- BEGIN service.https ---
// Credential is a UI login credential. Passwords are stored Argon2id-hashed.
type Credential struct {
	Username string `json:"username"`
	PassHash string `json:"passHash"`
	PassSalt string `json:"passSalt"`
	Perms    Perm   `json:"perms"`
}

// NormalizeUsername returns the canonical credential identity used for login,
// listing, removal, session attribution, and uniqueness checks.
func NormalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

// --- END service.https ---

func DefaultConfig(buildInfo build.BuildInfo) Configuration {
	// --- BEGIN service.https ---
	// New production and development configurations stay local by default.
	// Operators can still persist an explicit wildcard or LAN bind.
	uiBind := fmt.Sprintf("127.0.0.1:%d", buildInfo.ServiceDefaultPort)
	// --- END service.https ---

	return Configuration{
		LogLevel: buildInfo.DefaultLogLevel,
		// --- BEGIN service.https ---
		UIBind: uiBind,
		// --- END service.https ---
		// --- BEGIN update ---
		UpdateNotifications:    true,
		BackgroundUpdateChecks: true,
		LastUpdateCheck:        time.Time{},
		// --- END update ---
	}
}
