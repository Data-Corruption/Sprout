//go:build !windows

package maintenance

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestProbeJobDetectsRunnerKilledWithoutTrap reproduces the documented
// orphan: a real runner publishes its identity and is then SIGKILLed, so its
// EXIT trap never removes the job directory.
func TestProbeJobDetectsRunnerKilledWithoutTrap(t *testing.T) {
	jobDir := filepath.Join(t.TempDir(), "job")
	if err := os.Mkdir(jobDir, 0o700); err != nil {
		t.Fatal(err)
	}
	runner := filepath.Join(jobDir, unixRunnerName)
	script := fmt.Sprintf(`#!/bin/sh
job=%s
cleanup() { rm -rf "$job"; }
trap cleanup EXIT
printf '%%s\n' "$$" > "$job/%s"
sleep 30
`, shellQuote(jobDir), jobIdentityName)
	if err := os.WriteFile(runner, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", runner)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	admitted := time.Now()
	identity := filepath.Join(jobDir, jobIdentityName)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if data, err := os.ReadFile(identity); err == nil && len(data) > 0 {
			pid, err := strconv.Atoi(string(data[:len(data)-1]))
			if err != nil {
				t.Fatalf("runner identity %q: %v", data, err)
			}
			if pid != cmd.Process.Pid {
				t.Fatalf("runner recorded pid %d, started pid %d", pid, cmd.Process.Pid)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("runner did not publish its identity")
		}
		time.Sleep(10 * time.Millisecond)
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
		t.Fatalf("SIGKILL ran the cleanup trap, scenario invalid: %v", err)
	}
	status, err = ProbeJob(jobDir, admitted)
	if err != nil {
		t.Fatal(err)
	}
	if status != JobOrphaned {
		t.Fatalf("killed runner: ProbeJob() = %v, want JobOrphaned", status)
	}
}

// TestProbeJobRejectsRecycledPID points the identity at a live process that is
// not the runner: this test binary. kill(0) succeeds, argv does not name the
// runner, so the job is orphaned rather than running.
func TestProbeJobRejectsRecycledPID(t *testing.T) {
	jobDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(jobDir, jobIdentityName), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := ProbeJob(jobDir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if status != JobOrphaned {
		t.Fatalf("foreign live pid: ProbeJob() = %v, want JobOrphaned", status)
	}
}

func TestProbeJobRejectsExitedPID(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	jobDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(jobDir, jobIdentityName), []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := ProbeJob(jobDir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if status != JobOrphaned {
		t.Fatalf("reaped pid: ProbeJob() = %v, want JobOrphaned", status)
	}
}
