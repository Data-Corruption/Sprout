//go:build windows

package maintenance

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func currentProcessCreation(t *testing.T) uint64 {
	t.Helper()
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &creation, &exit, &kernel, &user); err != nil {
		t.Fatal(err)
	}
	return uint64(creation.HighDateTime)<<32 | uint64(creation.LowDateTime)
}

// TestProbeJobDetectsRunnerKilledWithoutFinally runs the runner's real
// identity statement in a PowerShell host, which also proves that .NET's
// StartTime round-trips to the FILETIME GetProcessTimes reports, then kills
// the host so its finally never removes the job directory.
func TestProbeJobDetectsRunnerKilledWithoutFinally(t *testing.T) {
	jobDir := filepath.Join(t.TempDir(), "job")
	if err := os.Mkdir(jobDir, 0o700); err != nil {
		t.Fatal(err)
	}
	command := fmt.Sprintf(`$JobDir = %s; $Utf8NoBom = New-Object Text.UTF8Encoding($false); %s; Start-Sleep -Seconds 60`,
		psLiteral(jobDir), windowsIdentityStatement)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", command)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	admitted := time.Now()
	identity := filepath.Join(jobDir, jobIdentityName)
	deadline := time.Now().Add(30 * time.Second)
	for {
		if data, err := os.ReadFile(identity); err == nil {
			if id, err := parseRunnerIdentity(data); err == nil && id.token != "" {
				if id.pid != cmd.Process.Pid {
					t.Fatalf("runner recorded pid %d, started pid %d", id.pid, cmd.Process.Pid)
				}
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("runner did not publish its identity")
		}
		time.Sleep(50 * time.Millisecond)
	}
	status, err := ProbeJob(jobDir, admitted)
	if err != nil {
		t.Fatal(err)
	}
	if status != JobRunning {
		t.Fatalf("live runner: ProbeJob() = %v, want JobRunning", status)
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if _, err := os.Stat(jobDir); err != nil {
		t.Fatalf("termination ran finally, scenario invalid: %v", err)
	}
	status, err = ProbeJob(jobDir, admitted)
	if err != nil {
		t.Fatal(err)
	}
	if status != JobOrphaned {
		t.Fatalf("killed runner: ProbeJob() = %v, want JobOrphaned", status)
	}
}

// TestProbeJobRequiresMatchingCreationTime points the identity at this live
// test process: the right creation time is running, a different one is a
// recycled PID and therefore orphaned.
func TestProbeJobRequiresMatchingCreationTime(t *testing.T) {
	created := currentProcessCreation(t)
	for _, tc := range []struct {
		token string
		want  JobStatus
	}{
		{strconv.FormatUint(created, 10), JobRunning},
		{strconv.FormatUint(created+1, 10), JobOrphaned},
	} {
		jobDir := t.TempDir()
		data := strconv.Itoa(os.Getpid()) + "\n" + tc.token + "\n"
		if err := os.WriteFile(filepath.Join(jobDir, jobIdentityName), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		status, err := ProbeJob(jobDir, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if status != tc.want {
			t.Fatalf("token %s: ProbeJob() = %v, want %v", tc.token, status, tc.want)
		}
	}
}

// TestProbeJobMissingCreationTimeIsTransientOnlyDuringGrace covers a reader
// that observes the pid line before the creation-time line has landed.
func TestProbeJobMissingCreationTimeIsTransientOnlyDuringGrace(t *testing.T) {
	jobDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(jobDir, jobIdentityName), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := ProbeJob(jobDir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if status != JobRunning {
		t.Fatalf("pid without creation time during grace: ProbeJob() = %v, want JobRunning", status)
	}
	if _, err := ProbeJob(jobDir, time.Now().Add(-2*jobStartGrace)); err == nil {
		t.Fatal("identity without creation time was accepted after grace")
	}
}
