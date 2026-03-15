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
		// fallback to DEBUG
		logLevel = "DEBUG"
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
	}
}
