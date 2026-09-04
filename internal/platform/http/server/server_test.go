// --- FILE service.https ---

package server

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"strings"
	"testing"
	"time"

	"sprout/internal/app"
	"sprout/pkg/xhttp"
)

func TestUnknownCertificateLogFilterSuppressesOnlyExpectedHandshakeError(t *testing.T) {
	var output bytes.Buffer
	logger := log.New(unknownCertificateLogFilter{dst: &output}, "", 0)

	suppressed := "http: TLS handshake error from 127.0.0.1:1234: remote error: tls: unknown certificate"
	otherHandshakeError := "http: TLS handshake error from 127.0.0.1:1235: EOF"
	otherUnknownCertificateError := "worker: remote error: tls: unknown certificate"
	logger.Print(suppressed)
	logger.Print(otherHandshakeError)
	logger.Print(otherUnknownCertificateError)

	got := output.String()
	if strings.Contains(got, suppressed) {
		t.Fatalf("expected unknown-certificate handshake error to be suppressed, got %q", got)
	}
	for _, want := range []string{otherHandshakeError, otherUnknownCertificateError} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q to be preserved, got %q", want, got)
		}
	}
}

func TestServeShutsDownBothListenersOnContextCancellation(t *testing.T) {
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	listening := make(chan struct{}, 2)
	newServer := func(name string) *xhttp.Server {
		srv, err := xhttp.NewServer(&xhttp.ServerConfig{
			Addr:             ":0",
			Handler:          handler,
			AfterListen:      func() { listening <- struct{}{} },
			AfterListenDelay: time.Millisecond,
		})
		if err != nil {
			t.Fatalf("new %s server: %v", name, err)
		}
		return srv
	}
	a := &app.App{Server: newServer("primary"), ProxyServer: newServer("proxy")}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, a)
	}()

	// Cancel only once both listeners are up so the test exercises a joint
	// shutdown rather than a cancellation during startup.
	for range 2 {
		select {
		case <-listening:
		case <-time.After(2 * time.Second):
			t.Fatal("listeners did not start")
		}
	}
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("listeners did not shut down together")
	}
}
