package config

import (
	"fmt"
	"net"
	"sprout/internal/types"
	"sprout/pkg/xlog"
	"strconv"
	"strings"
)

// ValidationError reports one or more invalid configuration values. It is a
// domain error; callers at transport boundaries decide how to present it.
type ValidationError struct {
	errs []error
}

func (e *ValidationError) Error() string {
	if e == nil {
		return ""
	}
	msgs := make([]string, 0, len(e.errs))
	for _, err := range e.errs {
		msgs = append(msgs, err.Error())
	}
	return strings.Join(msgs, "; ")
}

func (e *ValidationError) Unwrap() []error {
	if e == nil {
		return nil
	}
	return e.errs
}

// validate runs inside Update on the resulting configuration before it is
// persisted, so every writer (HTTP handlers, CLI commands, internal code)
// gets the same protection: a bad bind or log level would otherwise only
// surface as a failed listen on the next start.
func validate(cfg *types.Configuration) error {
	var errs []error

	if _, err := xlog.NormalizeLevel(cfg.LogLevel); err != nil {
		errs = append(errs, err)
	}
	// --- BEGIN service.https ---
	if err := validateBind(cfg.UIBind); err != nil {
		errs = append(errs, err)
	}
	// empty ProxyBind disables the proxy listener
	if pb := strings.TrimSpace(cfg.ProxyBind); pb != "" {
		if err := validateBind(pb); err != nil {
			errs = append(errs, err)
		} else if err := ValidateLoopbackBind(pb); err != nil {
			errs = append(errs, err)
		}
	}
	usernames := make(map[string]struct{}, len(cfg.Credentials))
	for _, credential := range cfg.Credentials {
		if credential.Username == "" {
			errs = append(errs, fmt.Errorf("credential username cannot be empty"))
			continue
		}
		if _, exists := usernames[credential.Username]; exists {
			errs = append(errs, fmt.Errorf("credential username %q is not unique", credential.Username))
			continue
		}
		usernames[credential.Username] = struct{}{}
	}
	// --- END service.https ---
	if len(errs) == 0 {
		return nil
	}
	return &ValidationError{errs: errs}
}

// --- BEGIN service.https ---
// validateBind checks that bind is a host:port with a valid numeric port,
// e.g. ":8484" or "0.0.0.0:8484".
func validateBind(bind string) error {
	_, portStr, err := net.SplitHostPort(bind)
	if err != nil {
		return fmt.Errorf("invalid bind %q (want host:port)", bind)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("invalid port %q in bind %q (want 1-65535)", portStr, bind)
	}
	return nil
}

// ValidateLoopbackBind rejects a proxy bind that is not loopback-only. The
// plain HTTP listener must never be reachable off-host. Exported for the
// server package's defense-in-depth check at listener start.
func ValidateLoopbackBind(bind string) error {
	host, _, err := net.SplitHostPort(bind)
	if err != nil {
		return fmt.Errorf("invalid proxy bind %q (want host:port): %w", bind, err)
	}
	host = strings.TrimSpace(host)
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("proxy bind host %q must be loopback (127.0.0.1, ::1, or localhost)", host)
	}
	return nil
}

// --- END service.https ---
