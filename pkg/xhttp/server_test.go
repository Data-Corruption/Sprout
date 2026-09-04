// --- FILE service.https ---

package xhttp

import (
	"context"
	"io"
	"log"
	"net/http"
	"testing"
	"time"
)

func noopHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {})
}

func TestNewServerValidation(t *testing.T) {
	t.Run("nil config", func(t *testing.T) {
		_, err := NewServer(nil)
		if err == nil {
			t.Fatal("expected error when config is nil")
		}
	})

	t.Run("nil handler", func(t *testing.T) {
		_, err := NewServer(&ServerConfig{})
		if err == nil {
			t.Fatalf("expected error when Handler is nil")
		}
	})

	t.Run("tls without cert paths", func(t *testing.T) {
		_, err := NewServer(&ServerConfig{
			UseTLS:  true,
			Handler: noopHandler(),
		})
		if err == nil {
			t.Fatalf("expected error when TLS paths are missing")
		}
	})
}

func TestNewServerDefaultsApplied(t *testing.T) {
	srv, err := NewServer(&ServerConfig{Handler: noopHandler()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := srv.cfg.Addr; got != DefaultAddr {
		t.Errorf("Addr default: want %q, got %q", DefaultAddr, got)
	}
	if got := srv.cfg.ReadTimeout; got != DefaultReadTimeout {
		t.Errorf("ReadTimeout default: want %s, got %s", DefaultReadTimeout, got)
	}
	if got := srv.cfg.WriteTimeout; got != DefaultWriteTimeout {
		t.Errorf("WriteTimeout default: want %s, got %s", DefaultWriteTimeout, got)
	}
	if got := srv.cfg.IdleTimeout; got != DefaultIdleTimeout {
		t.Errorf("IdleTimeout default: want %s, got %s", DefaultIdleTimeout, got)
	}
	if got := srv.cfg.ShutdownTimeout; got != DefaultShutdownTimeout {
		t.Errorf("ShutdownTimeout default: want %s, got %s", DefaultShutdownTimeout, got)
	}
	if got := srv.cfg.AfterListenDelay; got != DefaultAfterListenDelay {
		t.Errorf("AfterListenDelay default: want %s, got %s", DefaultAfterListenDelay, got)
	}

	if srv.server.TLSConfig == nil {
		t.Errorf("TLSConfig should never be nil (even for non-TLS servers)")
	}
}

func TestNewServerUsesConfiguredErrorLog(t *testing.T) {
	errorLog := log.New(io.Discard, "", 0)
	srv, err := NewServer(&ServerConfig{
		Handler:  noopHandler(),
		ErrorLog: errorLog,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if srv.server.ErrorLog != errorLog {
		t.Fatal("configured ErrorLog was not passed to net/http")
	}
}

func TestServeContextHandlesCancellationDuringStartup(t *testing.T) {
	srv, err := NewServer(&ServerConfig{Handler: noopHandler(), Addr: ":0"})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ServeContext(ctx)
	}()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("serve canceled context: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("server did not stop after startup cancellation")
	}
}

func TestNewServerTLSAddrDefault(t *testing.T) {
	srv, err := NewServer(&ServerConfig{
		UseTLS:      true,
		TLSKeyPath:  "./testdata/key.pem",
		TLSCertPath: "./testdata/cert.pem",
		Handler:     noopHandler(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := srv.cfg.Addr; got != DefaultTLSAddr {
		t.Errorf("TLS Addr default: want %q, got %q", DefaultTLSAddr, got)
	}
}

func TestServerServeLifecycle(t *testing.T) {
	listening := make(chan struct{})
	srv, err := NewServer(&ServerConfig{
		Handler:          noopHandler(),
		Addr:             ":0",
		AfterListen:      func() { close(listening) },
		AfterListenDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	done := make(chan struct{})
	go func() { _ = srv.Serve(); close(done) }()

	select {
	case <-listening:
	case <-time.After(2 * time.Second):
		t.Fatalf("listener did not start")
	}

	_ = srv.server.Close() // trigger graceful shutdown
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("server did not shut down in time")
	}
}
