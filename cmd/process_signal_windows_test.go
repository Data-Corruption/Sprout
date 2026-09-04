//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

const windowsConsoleEventHelper = "SPROUT_TEST_WINDOWS_CONSOLE_EVENT"

func TestProcessContextHandlesWindowsConsoleEvents(t *testing.T) {
	if event := os.Getenv(windowsConsoleEventHelper); event != "" {
		runWindowsConsoleEventHelper(t, event)
		return
	}

	for _, event := range []string{"ctrl-c", "ctrl-break", "ctrl-close"} {
		t.Run(event, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProcessContextHandlesWindowsConsoleEvents$")
			cmd.Env = append(os.Environ(), windowsConsoleEventHelper+"="+event)
			cmd.SysProcAttr = &syscall.SysProcAttr{
				CreationFlags: windows.CREATE_NEW_CONSOLE,
				HideWindow:    true,
			}
			output, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("helper timed out: %v\n%s", ctx.Err(), output)
			}
			if err != nil {
				t.Fatalf("helper failed: %v\n%s", err, output)
			}
			if !strings.Contains(string(output), "context canceled") {
				t.Fatalf("helper did not observe cancellation:\n%s", output)
			}
		})
	}
}

func runWindowsConsoleEventHelper(t *testing.T, event string) {
	t.Helper()
	ctx, stopSignals := notifyProcessContext(context.Background())
	defer stopSignals()

	var err error
	switch event {
	case "ctrl-c":
		err = windows.GenerateConsoleCtrlEvent(windows.CTRL_C_EVENT, 0)
	case "ctrl-break":
		err = windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, 0)
	case "ctrl-close":
		// Close is the safely reproducible member of the termination-event
		// branch that Go also uses for logoff and shutdown.
		err = postConsoleClose()
	default:
		t.Fatalf("unknown console event %q", event)
	}
	if err != nil {
		t.Fatalf("send %s: %v", event, err)
	}

	select {
	case <-ctx.Done():
		fmt.Println("context canceled")
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not cancel the process context", event)
	}
}

func postConsoleClose() error {
	getConsoleWindow := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow")
	handle, _, callErr := getConsoleWindow.Call()
	if handle == 0 {
		return fmt.Errorf("get console window: %v", callErr)
	}

	const wmClose = 0x0010
	postMessage := windows.NewLazySystemDLL("user32.dll").NewProc("PostMessageW")
	ok, _, callErr := postMessage.Call(handle, wmClose, 0, 0)
	if ok == 0 {
		return fmt.Errorf("post WM_CLOSE: %v", callErr)
	}
	return nil
}
