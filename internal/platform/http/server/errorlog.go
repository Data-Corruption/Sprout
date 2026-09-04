// --- FILE service.https ---

package server

import (
	"bytes"
	"io"
	"log"
)

var (
	tlsHandshakeLogPrefix       = []byte("http: TLS handshake error from ")
	unknownCertificateLogSuffix = []byte(": remote error: tls: unknown certificate")
)

// newHTTPErrorLogger preserves the standard logger's destination and formatting
// while suppressing the expected noise from clients rejecting our self-signed
// dashboard certificate.
func newHTTPErrorLogger() *log.Logger {
	base := log.Default()
	return log.New(
		unknownCertificateLogFilter{dst: base.Writer()},
		base.Prefix(),
		base.Flags(),
	)
}

type unknownCertificateLogFilter struct {
	dst io.Writer
}

func (w unknownCertificateLogFilter) Write(p []byte) (int, error) {
	line := bytes.TrimSuffix(p, []byte("\n"))
	if bytes.Contains(line, tlsHandshakeLogPrefix) &&
		bytes.HasSuffix(line, unknownCertificateLogSuffix) {
		return len(p), nil
	}
	return w.dst.Write(p)
}
