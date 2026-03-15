// Package sdnotify implements systemd sd_notify(3).
//
// It is a no-op if NOTIFY_SOCKET is unset.
package sdnotify

import (
	"net"
	"os"
	"time"
)

// notify sends k=v lines to systemd if NOTIFY_SOCKET is set.
// It is a no-op if NOTIFY_SOCKET is unset.
func notify(pairs map[string]string) error {
	addr := os.Getenv("NOTIFY_SOCKET")
	if addr == "" {
		return nil // not under systemd or Type!=notify
	}

	// Abstract namespace? systemd uses '@' prefix.
	if addr[0] == '@' {
		addr = "\x00" + addr[1:] // abstract: leading NUL
	}

	ua := &net.UnixAddr{Name: addr, Net: "unixgram"}
	conn, err := net.DialUnix("unixgram", nil, ua)
	if err != nil {
		return err
	}
	defer conn.Close()

	var msg []byte
	first := true
	for k, v := range pairs {
		if !first {
			msg = append(msg, '\n')
		}
		first = false
		msg = append(msg, k...)
		msg = append(msg, '=')
		msg = append(msg, v...)
	}

	// systemd expects this to be best-effort, fire-and-forget.
	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, err = conn.Write(msg)
	return err
}

func Ready(status string) error {
	pairs := map[string]string{"READY": "1"}
	if status != "" {
		pairs["STATUS"] = status
	}
	return notify(pairs)
}

func Stopping(status string) error {
	if status == "" {
		status = "Stopping"
	}
	return notify(map[string]string{"STOPPING": "1", "STATUS": status})
}

// Watchdog pokes the watchdog if WatchdogSec is configured in the unit.
// Call periodically <= WatchdogSec/2.
// Returns nil if NOTIFY_SOCKET unset (no-op).
// Not used by Sprout at this time.
func Watchdog() error {
	return notify(map[string]string{"WATCHDOG": "1"})
}
