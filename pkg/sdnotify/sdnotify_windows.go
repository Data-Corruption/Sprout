//go:build windows

// --- FILE service ---

// Package sdnotify provides no-op systemd lifecycle notifications on Windows.
package sdnotify

func Ready(string) error    { return nil }
func Stopping(string) error { return nil }
