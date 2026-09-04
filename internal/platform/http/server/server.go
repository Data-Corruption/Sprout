// --- FILE service.https ---

package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sprout/internal/app"
	"sprout/internal/platform/database/config"
	"sprout/internal/platform/secrets"
	"sprout/internal/types"
	"strings"

	"sprout/pkg/xhttp"
)

// New builds the dashboard's two HTTP listeners, both serving the same handler:
//   - a self-signed HTTPS listener on cfg.UIBind (the dashboard), and
//   - an optional loopback-only plain HTTP listener on cfg.ProxyBind, for a
//     local reverse proxy (e.g. Caddy) to terminate TLS in front of the app.
//
// [Serve] owns both listeners' serving and shutdown. ready is called only
// after the primary listener has started; the service coordinator combines
// that with worker readiness before publishing process readiness.
func New(ctx context.Context, a *app.App, cfg *types.Configuration, handler http.Handler, ready func()) error {
	secretStore, err := secrets.New(ctx, a.Layout.Data, a.BuildInfo().Name)
	if err != nil {
		return fmt.Errorf("failed to initialize dashboard secrets: %w", err)
	}
	a.Secrets = secretStore

	a.Server, err = xhttp.NewServer(&xhttp.ServerConfig{
		Addr:        cfg.UIBind,
		UseTLS:      true,
		TLSCertPath: a.Secrets.CertPath(),
		TLSKeyPath:  a.Secrets.KeyPath(),
		Handler:     handler,
		ErrorLog:    newHTTPErrorLogger(),
		AfterListen: func() {
			fmt.Println("Listening on", a.BaseURL) // for user
			a.Log.Infof("Listening on %s", a.Server.Addr())
			if ready != nil {
				ready()
			}
		},
	})
	if err != nil {
		return err
	}

	proxyBind := strings.TrimSpace(cfg.ProxyBind)
	if proxyBind == "" {
		return nil
	}
	// defense in depth: config.Update validates on writes
	if err := config.ValidateLoopbackBind(proxyBind); err != nil {
		return err
	}
	a.ProxyServer, err = xhttp.NewServer(&xhttp.ServerConfig{
		Addr:    proxyBind,
		UseTLS:  false,
		Handler: handler,
	})
	return err
}

// Serve runs the dashboard listeners until ctx is canceled or either listener
// fails. It does not subscribe to process signals.
func Serve(ctx context.Context, a *app.App) error {
	if a.Server == nil {
		return fmt.Errorf("primary server is not initialized")
	}

	type serveResult struct {
		name string
		err  error
	}
	serverCount := 1
	resultCh := make(chan serveResult, 2)
	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		resultCh <- serveResult{name: "dashboard", err: a.Server.ServeContext(serveCtx)}
	}()
	if a.ProxyServer != nil {
		serverCount++
		go func() {
			resultCh <- serveResult{name: "proxy", err: a.ProxyServer.ServeContext(serveCtx)}
		}()
	}

	var resultErr error
	resultsRead := 0
	select {
	case <-ctx.Done():
	case result := <-resultCh:
		resultsRead++
		if result.err == nil && ctx.Err() == nil {
			resultErr = fmt.Errorf("%s listener stopped unexpectedly", result.name)
		} else if result.err != nil {
			resultErr = fmt.Errorf("%s listener stopped: %w", result.name, result.err)
		}
	}
	cancel()

	for resultsRead < serverCount {
		result := <-resultCh
		resultsRead++
		if result.err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("%s listener stopped: %w", result.name, result.err))
		}
	}
	return resultErr
}
