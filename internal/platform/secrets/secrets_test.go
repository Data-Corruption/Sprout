// --- FILE service.https ---

package secrets

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestNewGeneratesLoadablePair(t *testing.T) {
	s, err := New(t.Context(), t.TempDir(), "test")
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if _, err := tls.LoadX509KeyPair(s.CertPath(), s.KeyPath()); err != nil {
		t.Fatalf("generated pair does not load: %v", err)
	}
}

func TestNewReusesExistingPair(t *testing.T) {
	dir := t.TempDir()
	s, err := New(t.Context(), dir, "test")
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	certBefore, err := os.ReadFile(s.CertPath())
	if err != nil {
		t.Fatalf("failed to read cert: %v", err)
	}

	if _, err := New(t.Context(), dir, "test"); err != nil {
		t.Fatalf("second New failed: %v", err)
	}
	certAfter, err := os.ReadFile(s.CertPath())
	if err != nil {
		t.Fatalf("failed to read cert: %v", err)
	}
	if string(certBefore) != string(certAfter) {
		t.Fatal("valid existing cert was regenerated; expected reuse to avoid browser trust churn")
	}
}

func TestValidateCertificate(t *testing.T) {
	now := time.Now()
	valid := x509.Certificate{
		NotBefore:   now.Add(-time.Hour),
		NotAfter:    now.Add(certRenewalWindow + time.Hour),
		DNSNames:    []string{"localhost", "old.example"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("192.0.2.1")},
	}
	if err := validateCertificate(&valid, now, []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}); err != nil {
		t.Fatalf("valid certificate rejected: %v", err)
	}

	tests := []struct {
		name string
		cert x509.Certificate
		dns  []string
		ips  []net.IP
	}{
		{
			name: "not yet valid",
			cert: x509.Certificate{NotBefore: now.Add(time.Hour), NotAfter: now.AddDate(1, 0, 0)},
		},
		{
			name: "near expiry",
			cert: x509.Certificate{NotBefore: now.Add(-time.Hour), NotAfter: now.Add(certRenewalWindow)},
		},
		{
			name: "missing current DNS SAN",
			cert: valid,
			dns:  []string{"new.example"},
		},
		{
			name: "missing current IP SAN",
			cert: valid,
			ips:  []net.IP{net.ParseIP("192.0.2.2")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateCertificate(&tt.cert, now, tt.dns, tt.ips); err == nil {
				t.Fatal("certificate unexpectedly accepted")
			}
		})
	}
}

func TestMismatchedPairIsRegenerated(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	sa, err := New(t.Context(), dirA, "test")
	if err != nil {
		t.Fatalf("New(A) failed: %v", err)
	}
	sb, err := New(t.Context(), dirB, "test")
	if err != nil {
		t.Fatalf("New(B) failed: %v", err)
	}

	// simulate an interrupted one-sided regeneration: A's cert paired with B's key
	foreignKey, err := os.ReadFile(sb.KeyPath())
	if err != nil {
		t.Fatalf("failed to read B key: %v", err)
	}
	if err := os.WriteFile(sa.KeyPath(), foreignKey, 0600); err != nil {
		t.Fatalf("failed to plant mismatched key: %v", err)
	}
	if _, err := tls.LoadX509KeyPair(sa.CertPath(), sa.KeyPath()); err == nil {
		t.Fatal("test setup broken: mismatched pair unexpectedly loads")
	}

	if _, err := New(t.Context(), dirA, "test"); err != nil {
		t.Fatalf("New over mismatched pair failed: %v", err)
	}
	if _, err := tls.LoadX509KeyPair(sa.CertPath(), sa.KeyPath()); err != nil {
		t.Fatalf("pair not healed after New: %v", err)
	}
}

func TestCorruptKeyIsRegenerated(t *testing.T) {
	dir := t.TempDir()
	s, err := New(t.Context(), dir, "test")
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if err := os.WriteFile(s.KeyPath(), []byte("not a pem file"), 0600); err != nil {
		t.Fatalf("failed to corrupt key: %v", err)
	}

	if _, err := New(t.Context(), dir, "test"); err != nil {
		t.Fatalf("New over corrupt key failed: %v", err)
	}
	if _, err := tls.LoadX509KeyPair(s.CertPath(), s.KeyPath()); err != nil {
		t.Fatalf("pair not healed after New: %v", err)
	}
}

func TestConcurrentFirstRunGeneratesMatchingPair(t *testing.T) {
	const processCount = 12

	storageDir := t.TempDir()
	barrier := filepath.Join(t.TempDir(), "start")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test executable: %v", err)
	}
	type child struct {
		cmd    *exec.Cmd
		output bytes.Buffer
	}
	children := make([]child, processCount)
	for i := range children {
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestConcurrentFirstRunHelper$")
		cmd.Env = append(os.Environ(),
			"SPROUT_SECRETS_HELPER=1",
			"SPROUT_SECRETS_STORAGE="+storageDir,
			"SPROUT_SECRETS_BARRIER="+barrier,
		)
		cmd.Stdout = &children[i].output
		cmd.Stderr = &children[i].output
		children[i].cmd = cmd
		if err := cmd.Start(); err != nil {
			t.Fatalf("start child %d: %v", i, err)
		}
	}

	if err := os.WriteFile(barrier, nil, 0o600); err != nil {
		t.Fatalf("release child barrier: %v", err)
	}
	for i := range children {
		if err := children[i].cmd.Wait(); err != nil {
			t.Errorf("child %d failed: %v\n%s", i, err, children[i].output.String())
		}
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("concurrent certificate test timed out: %v", err)
	}

	s := &Store{dir: filepath.Join(storageDir, "secrets")}
	if _, err := tls.LoadX509KeyPair(s.CertPath(), s.KeyPath()); err != nil {
		t.Fatalf("final pair does not load: %v", err)
	}
}

func TestConcurrentFirstRunHelper(t *testing.T) {
	if os.Getenv("SPROUT_SECRETS_HELPER") != "1" {
		return
	}
	barrier := os.Getenv("SPROUT_SECRETS_BARRIER")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(barrier); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat barrier: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for parent barrier")
		}
		time.Sleep(10 * time.Millisecond)
	}

	s, err := New(t.Context(), os.Getenv("SPROUT_SECRETS_STORAGE"), "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := tls.LoadX509KeyPair(s.CertPath(), s.KeyPath()); err != nil {
		t.Fatalf("load pair: %v", err)
	}
}
