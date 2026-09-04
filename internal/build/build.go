// Package build provides build-time information about the application.
package build

import (
	"encoding/json"
	"strconv"
)

// set by build.sh
var (
	name               string
	version            string
	contactURL         string
	defaultLogLevel    string
	serviceEnabled     string
	serviceDesc        string
	serviceArgs        string
	serviceDefaultPort string
	// Cosign identity is also required by the always-present uninstall recovery
	// launcher, so it is installation metadata rather than an update feature.
	certIdentity string
	oidcIssuer   string
	devMode      string
)

type BuildInfo struct {
	Name               string `json:"name"`
	Version            string `json:"version"`
	ContactURL         string `json:"contactURL"`
	DefaultLogLevel    string `json:"defaultLogLevel"`
	ServiceEnabled     bool   `json:"serviceEnabled"`
	ServiceDesc        string `json:"serviceDesc"`
	ServiceArgs        string `json:"serviceArgs"`
	ServiceDefaultPort int    `json:"serviceDefaultPort"`
	// CertIdentity / OidcIssuer pin the cosign keyless identity used to
	// verify a detached maintenance installer before it executes. The identity
	// includes the repository and workflow path, so it is unforgeable.
	CertIdentity string `json:"certIdentity"`
	OidcIssuer   string `json:"oidcIssuer"`
	// DevMode bypasses HTTP auth, isolates storage in "-dev" dirs, and
	// forces debug logging. Only ever true in local dev builds (the default
	// build.sh mode); production builds never set it.
	DevMode bool `json:"devMode"`
}

// PrintJSON prints the build info as JSON to stdout
func (b BuildInfo) PrintJSON() string {
	data, err := json.Marshal(b)
	if err != nil {
		return ""
	}
	return string(data)
}

func Info() BuildInfo {
	port, err := strconv.Atoi(serviceDefaultPort)
	if err != nil {
		// fallback to 8080
		port = 8080
	}
	logLevel := defaultLogLevel
	if logLevel == "" {
		// fallback to canonical debug level
		logLevel = "debug"
	}
	return BuildInfo{
		Name:               name,
		Version:            version,
		ContactURL:         contactURL,
		DefaultLogLevel:    logLevel,
		ServiceEnabled:     serviceEnabled == "true",
		ServiceDesc:        serviceDesc,
		ServiceArgs:        serviceArgs,
		ServiceDefaultPort: port,
		CertIdentity:       certIdentity,
		OidcIssuer:         oidcIssuer,
		DevMode:            devMode == "true",
	}
}
