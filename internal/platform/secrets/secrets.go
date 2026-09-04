// --- FILE service.https ---

// Package secrets manages control-plane secrets that live outside the main
// config blob: currently the self-signed dashboard TLS certificate/key.
//
// Everything lives in a 0700 directory beside the database. The TLS key is
// 0600; the cert is 0644 and reused across restarts to avoid browser trust
// churn. Existing material with looser permissions is rejected, never
// repaired. Apps with more secret material (API keys, master keys, ...) should
// add it here rather than in the database config.
package secrets

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"sprout/pkg/xsyscall"
)

const (
	certFile          = "cert.pem"
	keyFile           = "key.pem"
	certRenewalWindow = 30 * 24 * time.Hour
	// Generating a keypair takes milliseconds, so a peer holding this lock for
	// 30 seconds is wedged. This runs on the HTTPS startup path, where waiting
	// forever would be a hang rather than a diagnosable failure.
	certLockTimeout = 30 * time.Second
)

// Store owns immutable paths to the on-disk secret material. New serializes
// certificate validation and regeneration across processes.
type Store struct {
	dir string
}

// New opens (creating if needed) the secrets directory under storageDir and
// ensures a TLS certificate exists. commonName is baked into the generated
// cert (typically the app name). It gives up if ctx is cancelled or another
// process holds the certificate lock for longer than certLockTimeout.
func New(ctx context.Context, storageDir, commonName string) (*Store, error) {
	dir := filepath.Join(storageDir, "secrets")
	if err := ensureSecretsDir(dir); err != nil {
		return nil, fmt.Errorf("failed to prepare secrets dir: %w", err)
	}
	lock, err := xsyscall.AcquireLock(ctx, filepath.Join(dir, ".cert.lock"), xsyscall.LockOptions{
		Mode:    xsyscall.ModeExclusive,
		Timeout: certLockTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to lock secrets dir: %w", err)
	}
	defer lock.Close()

	s := &Store{dir: dir}
	if err := s.ensureCert(commonName); err != nil {
		return nil, err
	}
	return s, nil
}

// CertPath and KeyPath return the dashboard TLS certificate/key file paths.
func (s *Store) CertPath() string { return filepath.Join(s.dir, certFile) }
func (s *Store) KeyPath() string  { return filepath.Join(s.dir, keyFile) }

func (s *Store) ensureCert(commonName string) error {
	certPath, keyPath := s.CertPath(), s.KeyPath()
	_, cerr := os.Stat(certPath)
	_, kerr := os.Stat(keyPath)
	if cerr == nil && kerr == nil {
		// An existing pair with loose permissions is rejected, not repaired:
		// this code wrote it with the right mode, so a wrong one means
		// something else touched the key material.
		if err := checkSecretFile(keyPath, 0o600); err != nil {
			return fmt.Errorf("refusing existing TLS key: %w", err)
		}
		if err := checkSecretFile(certPath, 0o644); err != nil {
			return fmt.Errorf("refusing existing TLS certificate: %w", err)
		}
		pair, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err == nil {
			leaf := pair.Leaf
			if leaf == nil {
				leaf, err = x509.ParseCertificate(pair.Certificate[0])
			}
			if err == nil {
				dnsNames, ips := detectSANs()
				err = validateCertificate(leaf, time.Now(), dnsNames, ips)
			}
			if err == nil {
				return nil
			}
		}
		// HTTPS startup has not completed yet; stderr is the best available
		// diagnostic path.
		fmt.Fprintf(os.Stderr, "warning: dashboard TLS certificate needs regeneration (%v)\n", err)
	}
	return generateSelfSigned(certPath, keyPath, commonName)
}

func validateCertificate(cert *x509.Certificate, now time.Time, dnsNames []string, ips []net.IP) error {
	if now.Before(cert.NotBefore) {
		return fmt.Errorf("not valid until %s", cert.NotBefore.Format(time.RFC3339))
	}
	if !now.Add(certRenewalWindow).Before(cert.NotAfter) {
		return fmt.Errorf("expires too soon at %s", cert.NotAfter.Format(time.RFC3339))
	}
	for _, name := range dnsNames {
		if err := cert.VerifyHostname(name); err != nil {
			return fmt.Errorf("missing DNS SAN %q", name)
		}
	}
	for _, ip := range ips {
		if err := cert.VerifyHostname(ip.String()); err != nil {
			return fmt.Errorf("missing IP SAN %q", ip)
		}
	}
	return nil
}

// generateSelfSigned writes a fresh self-signed ECDSA P-256 certificate/key
// pair with SANs for localhost, loopback, and the host's detected addresses.
func generateSelfSigned(certPath, keyPath, commonName string) error {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("failed to generate EC key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("failed to generate serial: %w", err)
	}

	dnsNames, ips := detectSANs()
	now := time.Now()
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dnsNames,
		IPAddresses:           ips,
	}

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return fmt.Errorf("failed to create certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return fmt.Errorf("failed to marshal EC key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		return fmt.Errorf("generated TLS pair does not match: %w", err)
	}

	certTemp, err := writeTempFile(filepath.Dir(certPath), ".cert-*.tmp", certPEM, 0o644)
	if err != nil {
		return fmt.Errorf("failed to stage certificate: %w", err)
	}
	defer os.Remove(certTemp)
	keyTemp, err := writeTempFile(filepath.Dir(keyPath), ".key-*.tmp", keyPEM, 0o600)
	if err != nil {
		return fmt.Errorf("failed to stage private key: %w", err)
	}
	defer os.Remove(keyTemp)

	if _, err := tls.LoadX509KeyPair(certTemp, keyTemp); err != nil {
		return fmt.Errorf("staged TLS pair does not match: %w", err)
	}
	if err := replaceFile(certTemp, certPath); err != nil {
		return fmt.Errorf("failed to publish certificate: %w", err)
	}
	if err := replaceFile(keyTemp, keyPath); err != nil {
		return fmt.Errorf("failed to publish private key: %w", err)
	}
	if _, err := tls.LoadX509KeyPair(certPath, keyPath); err != nil {
		return fmt.Errorf("published TLS pair does not match: %w", err)
	}
	return nil
}

// detectSANs returns DNS names and IPs to bake into the dashboard cert: always
// localhost + loopback, plus the hostname and non-loopback interface addresses
// where practical.
func detectSANs() ([]string, []net.IP) {
	dnsNames := []string{"localhost"}
	ips := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}

	if host, err := os.Hostname(); err == nil && host != "" && host != "localhost" {
		dnsNames = append(dnsNames, host)
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok || ipNet.IP.IsLoopback() || ipNet.IP.IsLinkLocalUnicast() {
				continue
			}
			ips = append(ips, ipNet.IP)
		}
	}
	return dnsNames, ips
}

func writeTempFile(dir, pattern string, data []byte, perm os.FileMode) (path string, err error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	path = f.Name()
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(path)
		}
	}()
	if err = f.Chmod(perm); err != nil {
		return "", err
	}
	if _, err = f.Write(data); err != nil {
		return "", err
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	return path, nil
}
