package maintenance

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProbeJobMissingDirectoryIsGone(t *testing.T) {
	status, err := ProbeJob(filepath.Join(t.TempDir(), "absent"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if status != JobGone {
		t.Fatalf("ProbeJob() = %v, want JobGone", status)
	}
}

func TestProbeJobMissingIdentityHonoursStartGrace(t *testing.T) {
	jobDir := t.TempDir()
	status, err := ProbeJob(jobDir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if status != JobRunning {
		t.Fatalf("fresh admission without identity: ProbeJob() = %v, want JobRunning", status)
	}
	status, err = ProbeJob(jobDir, time.Now().Add(-2*jobStartGrace))
	if err != nil {
		t.Fatal(err)
	}
	if status != JobOrphaned {
		t.Fatalf("expired grace without identity: ProbeJob() = %v, want JobOrphaned", status)
	}
}

func TestProbeJobMalformedIdentityIsTransientOnlyDuringGrace(t *testing.T) {
	jobDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(jobDir, jobIdentityName), []byte("not-a-pid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := ProbeJob(jobDir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if status != JobRunning {
		t.Fatalf("partial identity during grace: ProbeJob() = %v, want JobRunning", status)
	}
	if _, err := ProbeJob(jobDir, time.Now().Add(-2*jobStartGrace)); err == nil {
		t.Fatal("malformed identity after grace did not surface an error")
	}
}

func TestParseRunnerIdentityAcceptsBothLineEndings(t *testing.T) {
	for _, data := range []string{"4242\n", "4242\r\n", "4242\r\n133000000000000000\r\n", "4242\n133000000000000000\n"} {
		id, err := parseRunnerIdentity([]byte(data))
		if err != nil {
			t.Fatalf("%q: %v", data, err)
		}
		if id.pid != 4242 {
			t.Fatalf("%q: pid = %d", data, id.pid)
		}
		if len(data) > 6 && id.token != "133000000000000000" {
			t.Fatalf("%q: token = %q", data, id.token)
		}
	}
	for _, data := range []string{"", "\n", "0\n", "-5\n", "abc\n"} {
		if _, err := parseRunnerIdentity([]byte(data)); err == nil {
			t.Fatalf("%q parsed as a valid identity", data)
		}
	}
}
