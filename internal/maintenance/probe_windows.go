//go:build windows

package maintenance

import (
	"errors"
	"fmt"
	"strconv"

	"golang.org/x/sys/windows"
)

const windowsRunnerName = "run.ps1"

// windowsIdentityStatement is the PowerShell that publishes the runner's
// identity. It relies on $JobDir and $Utf8NoBom being defined by the runner.
// PIDs recycle quickly on Windows, so the creation time, which together with
// the PID uniquely names a process, is recorded as the second line. .NET's
// Process.StartTime is GetProcessTimes' creation time round-tripped through a
// local DateTime; ToFileTimeUtc restores the exact FILETIME.
const windowsIdentityStatement = `[IO.File]::WriteAllText((Join-Path $JobDir "` + jobIdentityName + `"), ($PID.ToString() + "` + "`n" + `" + (Get-Process -Id $PID).StartTime.ToFileTimeUtc().ToString() + "` + "`n" + `"), $Utf8NoBom)`

// stillActive is STATUS_PENDING, which GetExitCodeProcess reports for a
// process that has not exited. x/sys/windows does not export it.
const stillActive = 259

// runnerAlive decides whether the PID recorded by the runner still belongs to
// it. PROCESS_QUERY_LIMITED_INFORMATION is granted for any same-user process
// without elevation; a denied open is therefore a process that cannot be the
// runner. The creation time comparison rejects a recycled PID.
func runnerAlive(_ string, id runnerIdentity) (bool, error) {
	created, err := strconv.ParseUint(id.token, 10, 64)
	if err != nil {
		return false, fmt.Errorf("%w: creation time %q: %v", errIncompleteIdentity, id.token, err)
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(id.pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return false, nil
		}
		return false, fmt.Errorf("open maintenance runner %d: %w", id.pid, err)
	}
	defer windows.CloseHandle(handle)
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return false, fmt.Errorf("query maintenance runner %d times: %w", id.pid, err)
	}
	if uint64(creation.HighDateTime)<<32|uint64(creation.LowDateTime) != created {
		return false, nil
	}
	// A process object stays queryable while any handle to it is open, so the
	// PID may still be reserved after the runner exited.
	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return false, fmt.Errorf("query maintenance runner %d exit code: %w", id.pid, err)
	}
	return code == stillActive, nil
}
