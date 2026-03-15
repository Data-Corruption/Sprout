package types

import (
	"sprout/internal/build"
	"time"
)

type Configuration struct {
	LogLevel  string `json:"logLevel"`
	Port      int    `json:"port"`      // port the server is listening on. 80/443 will be omitted from URLs
	Host      string `json:"host"`      // host the server is listening on
	ProxyPort int    `json:"proxyPort"` // port the proxy is listening on, 0 = no proxy. 80/443 will be omitted from URLs

	UpdateNotifications bool      `json:"updateNotifications"`
	LastUpdateCheck     time.Time `json:"lastUpdateCheck"`
	UpdateAvailable     bool      `json:"updateAvailable"`

	// app version when update process was accepted. This is lazily used to determine if the update was successful after restart.
	PreUpdateVersion string `json:"preUpdateVersion"`
	// incremented on each service start (usually server listen or similar), used for detecting restarts
	StartCounter int `json:"startCounter"`
}

func DefaultConfig() Configuration {
	return Configuration{
		LogLevel:            build.Info().DefaultLogLevel,
		Port:                build.Info().ServiceDefaultPort,
		Host:                "localhost",
		UpdateNotifications: true,
		LastUpdateCheck:     time.Time{},
	}
}
