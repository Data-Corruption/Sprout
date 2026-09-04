// --- FILE service.https ---

// Package xhttp extends net/http with a few independent and opt-in abstractions:
//   - [Server] wraps [http.Server] with signal-based graceful shutdown, lifecycle hooks, and sensible defaults
//   - [Err] type and [Error] function for separating internal errors from client-safe messages in HTTP handlers
//
// [Server] usage:
//
//	srv, err := xhttp.NewServer(&xhttp.ServerConfig{
//		UseTLS:      true,
//		TLSCertPath: "./cert.pem",
//		TLSKeyPath:  "./key.pem",
//		Handler:     myHandler,
//		AfterListen: func() { /* do something after listen */ },
//		AfterListenDelay: 2 * time.Second, // delay before calling AfterListen, defaults to 1 second
//		OnShutdown:  func() { /* cleanup database connections, websockets, etc. */ },
//		// See [ServerConfig] for all options and defaults.
//	})
//	if err != nil {
//		log.Fatalf("failed to create server: %v", err)
//	}
//	log.Fatal(srv.Listen())
//
// [Err] and [Error] usage:
//
//	func SubFunc() error {
//		// do something that might fail with sensitive info in the error
//		_, err := sensitiveFoo()
//		if err != nil {
//			return &xhttp.Err{Code: 500, Msg: "An error occurred doing foo", Err: err}
//		}
//		return nil
//	}
//
//	func HandlerFunc(w http.ResponseWriter, r *http.Request) {
//		ctx := r.Context() // should contain sprout/pkg/xlog logger, skips logging if not present
//		if err := SubFunc(); err != nil {
//			// use [Error] instead of [http.Error]. It logs the error and sends an
//			// appropriate HTTP response, defaulting to 500, "Internal Server Error". If not an [Err].
//			xhttp.Error(ctx, w, err)
//			return
//		}
//		// continue handling the request
//	}
package xhttp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Default values for config, everything else defaults to zero values.
const (
	DefaultAddr             = ":80"
	DefaultTLSAddr          = ":443"
	DefaultReadTimeout      = 5 * time.Second
	DefaultWriteTimeout     = 10 * time.Second
	DefaultIdleTimeout      = 120 * time.Second
	DefaultShutdownTimeout  = 10 * time.Second
	DefaultAfterListenDelay = 1 * time.Second
)

// ServerConfig holds configuration options for [Server].
type ServerConfig struct {
	Addr string // Address to listen on (e.g. ":8080"). Default is ":80", ":443" if UseTLS is true.

	UseTLS      bool   // Whether to use TLS (HTTPS). If true, TLSKeyPath and TLSCertPath must be set.
	TLSKeyPath  string // Path to the TLS private key file.
	TLSCertPath string // Path to the TLS certificate file.

	// Handler, typically a router or middleware chain. Required.
	//
	// Works with any http.Handler compatible router (chi, gorilla/mux, etc.)
	Handler http.Handler

	// ErrorLog specifies the logger used by net/http for server errors.
	// Nil uses net/http's default logger.
	ErrorLog *log.Logger

	ReadTimeout  time.Duration // Max duration for reading the entire request, including the body. Default is 5 seconds. Negative to disable.
	WriteTimeout time.Duration // Max duration before timing out writes of the response. Default is 10 seconds. Negative to disable.

	// IdleTimeout is the maximum duration for which an idle connection will remain open.
	// In plain terms, [http.Server] leaves connections open for a certain time after the last request
	// for performance reasons. This is the maximum duration for that. Default is 120 seconds. Negative to disable.
	//
	// This does not affect:
	//  - WebSocket connections (once upgraded)
	//  - Active request/response handling
	//  - Long-lived streaming responses (like SSE or chunked transfer)
	IdleTimeout time.Duration

	ShutdownTimeout time.Duration // Maximum duration for graceful shutdown. Default is 10 seconds. Negative to disable (connections are closed immediately and OnShutdown is not registered).

	// AfterListen, if non-nil, is called after the server starts listening. Simple and flexible.
	// Useful for validating the server is up and running, e.g. by checking a health endpoint.
	AfterListen      func()
	AfterListenDelay time.Duration // Delay after starting the server before calling AfterListen.

	// OnShutdown, if non-nil, is called during server shutdown, after the
	// server has stopped accepting new connections, but before closing idle ones.
	//
	// Notes:
	//  - depending on the shutdown timeout, this may exceed the life of the server.
	//  - if ShutdownTimeout is <= 0, this will not be called.
	OnShutdown func()
}

// Server wraps [http.Server] with graceful shutdown, lifecycle hooks, and sensible defaults.
type Server struct {
	cfg    *ServerConfig // Configuration for the server
	server *http.Server  // The http or https server
}

// NewServer creates a new Server instance with the provided configuration.
func NewServer(cfg *ServerConfig) (*Server, error) {
	if cfg == nil {
		return nil, fmt.Errorf("server config must be provided")
	}
	copy := *cfg

	if copy.Handler == nil {
		return nil, fmt.Errorf("handler must be provided")
	}

	if copy.UseTLS && (copy.TLSKeyPath == "" || copy.TLSCertPath == "") {
		return nil, fmt.Errorf("TLS key and cert paths must be provided when using TLS")
	}

	// set defaults

	if copy.Addr == "" {
		if copy.UseTLS {
			copy.Addr = DefaultTLSAddr
		} else {
			copy.Addr = DefaultAddr
		}
	}

	if copy.ReadTimeout == 0 {
		copy.ReadTimeout = DefaultReadTimeout
	}
	if copy.WriteTimeout == 0 {
		copy.WriteTimeout = DefaultWriteTimeout
	}
	if copy.IdleTimeout == 0 {
		copy.IdleTimeout = DefaultIdleTimeout
	}
	if copy.ShutdownTimeout == 0 {
		copy.ShutdownTimeout = DefaultShutdownTimeout
	}
	if copy.AfterListenDelay == 0 {
		copy.AfterListenDelay = DefaultAfterListenDelay
	}

	// create http server
	httpServer := &http.Server{
		Addr:         copy.Addr,
		Handler:      copy.Handler,
		ReadTimeout:  copy.ReadTimeout,
		WriteTimeout: copy.WriteTimeout,
		IdleTimeout:  copy.IdleTimeout,
		TLSConfig:    &tls.Config{MinVersion: tls.VersionTLS13},
		ErrorLog:     copy.ErrorLog,
	}

	// set shutdown hook if provided
	if copy.OnShutdown != nil && copy.ShutdownTimeout > 0 {
		httpServer.RegisterOnShutdown(copy.OnShutdown)
	}

	// return the server
	return &Server{
		cfg:    &copy,
		server: httpServer,
	}, nil
}

// Addr returns the address the server is listening on.
func (s *Server) Addr() string {
	if s.cfg == nil {
		return ""
	}
	return s.cfg.Addr
}

func (s *Server) listen() (net.Listener, error) {
	listener, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return nil, fmt.Errorf("address already in use: %w", err)
		}
		if errors.Is(err, syscall.EACCES) {
			return nil, fmt.Errorf("permission denied: %w", err)
		}
		return nil, err
	}
	if !s.cfg.UseTLS {
		return listener, nil
	}
	certificate, err := tls.LoadX509KeyPair(s.cfg.TLSCertPath, s.cfg.TLSKeyPath)
	if err != nil {
		listener.Close()
		return nil, err
	}
	tlsConfig := s.server.TLSConfig.Clone()
	tlsConfig.Certificates = []tls.Certificate{certificate}
	return tls.NewListener(listener, tlsConfig), nil
}

func (s *Server) serve(listener net.Listener) error {
	var afterListenTimer *time.Timer
	if s.cfg.AfterListen != nil {
		afterListenTimer = time.AfterFunc(s.cfg.AfterListenDelay, s.cfg.AfterListen)
		defer afterListenTimer.Stop()
	}

	err := s.server.Serve(listener)
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Serve starts the server and blocks until it is shut down or an error occurs.
// It does not subscribe to process signals.
func (s *Server) Serve() error {
	listener, err := s.listen()
	if err != nil {
		return err
	}
	defer listener.Close()
	return s.serve(listener)
}

// ServeContext serves until ctx is canceled, the server is shut down, or
// serving fails. Closing the listener before graceful shutdown also makes
// cancellation safe if it arrives during startup.
func (s *Server) ServeContext(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("context is nil")
	}
	listener, err := s.listen()
	if err != nil {
		return err
	}
	defer listener.Close()

	cancelDone := make(chan struct{})
	shutdownErrCh := make(chan error, 1)
	go func() {
		select {
		case <-ctx.Done():
			_ = listener.Close()
			shutdownErrCh <- s.Shutdown()
		case <-cancelDone:
			shutdownErrCh <- nil
		}
	}()

	serveErr := s.serve(listener)
	close(cancelDone)
	shutdownErr := <-shutdownErrCh
	if ctx.Err() != nil {
		if errors.Is(serveErr, net.ErrClosed) {
			serveErr = nil
		}
		if errors.Is(shutdownErr, net.ErrClosed) {
			shutdownErr = nil
		}
	}
	return errors.Join(serveErr, shutdownErr)
}

// Listen serves until the process is interrupted, the server is shut down, or
// serving fails. Use Serve for secondary listeners that must not own signals.
func (s *Server) Listen() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return s.ServeContext(ctx)
}

// Shutdown gracefully stops the server, blocking until all connections are
// closed or the provided context times out or is canceled.
//
// Thread-safe, can be called from any goroutine.
func (s *Server) ShutdownWithContext(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("context is nil") // prevents panic when there are active connections / cleanup wait
	}
	return s.server.Shutdown(ctx) // blocks
}

// Shutdown gracefully stops the server, blocking until all connections are
// closed or the server's shutdown timeout is reached.
//
// Thread-safe, can be called from any goroutine.
func (s *Server) Shutdown() error {
	if s.cfg.ShutdownTimeout <= 0 {
		return s.server.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
	defer cancel()
	return s.server.Shutdown(ctx) // blocks
}
