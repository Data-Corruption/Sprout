// --- FILE service ---

package commands

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"sprout/internal/app"
	"sprout/internal/platform/database/config"
	"sprout/internal/platform/http/router"
	"sprout/internal/platform/http/server"
	"sprout/internal/types"
	"sprout/internal/ui"
	"sprout/pkg/sdnotify"

	"github.com/urfave/cli/v3"
)

const serviceControlTimeout = 45 * time.Second

var (
	runWorkerComponent = runWorker
	// --- BEGIN service.https ---
	runHTTPComponent = server.Serve
	// --- END service.https ---
)

func serviceCommand(a *app.App) *cli.Command {
	if !a.BuildInfo().ServiceEnabled {
		return nil
	}
	// --- BEGIN service.https ---
	var serviceConfig *types.Configuration
	var portOverride int
	// --- END service.https ---
	return &cli.Command{
		Name:  "service",
		Usage: "service management commands",
		// --- BEGIN service.https ---
		Before: func(ctx context.Context, _ *cli.Command) (context.Context, error) {
			cfg, err := serviceRunConfiguration(a, portOverride)
			if err != nil {
				return ctx, err
			}
			serviceConfig = cfg
			a.BaseURL = bindToBaseURL(cfg.UIBind, a.BuildInfo().ServiceDefaultPort)
			a.Log.Debugf("Base URL: %s", a.BaseURL)
			return ctx, nil
		},
		// --- END service.https ---
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if a.BuildInfo().Name == "" || a.Layout.Storage == "" {
				return fmt.Errorf("app name or storage path not found")
			}
			printServiceHelp(a)
			return nil
		},
		Commands: []*cli.Command{
			(serviceCommandBuilder{a: a}).control("start", "start the managed service"),
			(serviceCommandBuilder{a: a}).control("stop", "stop the managed service"),
			(serviceCommandBuilder{a: a}).control("restart", "restart the managed service"),
			(serviceCommandBuilder{a: a}).control("status", "show managed service status"),
			{
				Name:        "run",
				Hidden:      true,
				Description: "Runs service in foreground. Typically called by systemd. If you need to run it manually/unmanaged, use this command.",
				// --- BEGIN service.https ---
				Flags: []cli.Flag{
					&cli.IntFlag{
						Name:        "port",
						Aliases:     []string{"p"},
						Usage:       "temporarily override the dashboard port for this run",
						Destination: &portOverride,
					},
				},
				// --- END service.https ---
				Action: func(ctx context.Context, cmd *cli.Command) (runErr error) {
					serviceLock, err := a.AcquireServiceLock()
					if err != nil {
						return fmt.Errorf("cannot run service: %w", err)
					}
					defer func() {
						runErr = errors.Join(runErr, serviceLock.Close())
					}()

					// --- BEGIN service.https ---
					dashboardUI, err := ui.New()
					if err != nil {
						return fmt.Errorf("failed to load UI: %w", err)
					}
					a.UI = dashboardUI

					mux := router.New(a)
					httpReady := make(chan struct{}, 1)
					if err := server.New(ctx, a, serviceConfig, mux, func() { httpReady <- struct{}{} }); err != nil {
						return fmt.Errorf("failed to create server: %w", err)
					}
					if a.ProxyServer != nil {
						a.Log.Infof("Proxy listener on %s", a.ProxyServer.Addr())
					}
					// --- END service.https ---

					return runService(ctx, a,
						// --- BEGIN service.https ---
						httpReady,
						// --- END service.https ---
					)
				},
			},
		},
	}
}

type serviceCommandBuilder struct {
	a *app.App
}

func (b serviceCommandBuilder) control(action, usage string) *cli.Command {
	return &cli.Command{
		Name:  action,
		Usage: usage,
		Action: func(ctx context.Context, _ *cli.Command) error {
			ctx, cancel := context.WithTimeout(ctx, serviceControlTimeout)
			defer cancel()
			output, err := controlService(ctx, b.a, action)
			if output != "" {
				fmt.Println(output)
			}
			return err
		},
	}
}

type componentResult struct {
	name string
	err  error
}

// component is one long-running part of the service. run must call ready once
// its startup preflight is done and return promptly when ctx is cancelled.
type component struct {
	name string
	run  func(ctx context.Context, ready func()) error
}

// componentGroup runs components concurrently and collects one readiness
// signal and one result from each. Both channels are sized to the component
// count and every component signals each at most once, so no send can block
// after the collector has stopped listening.
type componentGroup struct {
	ctx       context.Context
	readyCh   chan struct{}
	resultCh  chan componentResult
	count     int
	completed int
}

func startComponents(ctx context.Context, components []component) *componentGroup {
	g := &componentGroup{
		ctx:      ctx,
		readyCh:  make(chan struct{}, len(components)),
		resultCh: make(chan componentResult, len(components)),
		count:    len(components),
	}
	for _, c := range components {
		go func() {
			var readyOnce sync.Once
			ready := func() {
				readyOnce.Do(func() { g.readyCh <- struct{}{} })
			}
			g.resultCh <- componentResult{name: c.name, err: c.run(ctx, ready)}
		}()
	}
	return g
}

// awaitReadiness returns once every component has signalled ready, the
// service context is cancelled, or a component ended first. A result seen
// here is judged by unexpectedComponentError; a nil return with a cancelled
// context means the service stopped before readiness could be published.
func (g *componentGroup) awaitReadiness() error {
	for ready := 0; ready < g.count; {
		select {
		case <-g.ctx.Done():
			return nil
		case <-g.readyCh:
			ready++
		case result := <-g.resultCh:
			g.completed++
			return unexpectedComponentError(g.ctx, result)
		}
	}
	// A component may have stopped in the same instant its last peer became
	// ready; do not publish readiness over a result that is already waiting.
	select {
	case result := <-g.resultCh:
		g.completed++
		return unexpectedComponentError(g.ctx, result)
	default:
		return nil
	}
}

// waitForStop blocks until the service is cancelled or a component ends.
func (g *componentGroup) waitForStop() error {
	select {
	case <-g.ctx.Done():
		return nil
	case result := <-g.resultCh:
		g.completed++
		return unexpectedComponentError(g.ctx, result)
	}
}

// drain waits for every remaining component to return after cancellation and
// joins any failure that is not the cancellation itself.
func (g *componentGroup) drain(joined error) error {
	for g.completed < g.count {
		result := <-g.resultCh
		g.completed++
		if result.err != nil && !errors.Is(result.err, context.Canceled) {
			joined = errors.Join(joined, fmt.Errorf("%s stopped: %w", result.name, result.err))
		}
	}
	return joined
}

func runService(
	ctx context.Context,
	a *app.App,
	// --- BEGIN service.https ---
	httpReady <-chan struct{},
	// --- END service.https ---
) error {
	serviceCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	components := []component{
		{name: "worker", run: func(ctx context.Context, ready func()) error {
			return runWorkerComponent(ctx, a, ready)
		}},
		{name: "service stop watcher", run: func(ctx context.Context, ready func()) error {
			return a.RunServiceStopWatcher(ctx, cancel, ready)
		}},
		// --- BEGIN update ---
		{name: "update checker", run: a.RunUpdateChecker},
		// --- END update ---
		// --- BEGIN service.https ---
		{name: "dashboard", run: func(ctx context.Context, ready func()) error {
			go func() {
				select {
				case <-ctx.Done():
				case <-httpReady:
					ready()
				}
			}()
			return runHTTPComponent(ctx, a)
		}},
		// --- END service.https ---
	}
	group := startComponents(serviceCtx, components)

	joined := group.awaitReadiness()
	// Do not publish service readiness if a component stopped or cancelled the
	// service while readiness notifications were being collected.
	if joined == nil && serviceCtx.Err() == nil {
		joined = publishServiceReady(a)
		if joined == nil {
			joined = group.waitForStop()
		}
	}

	if err := sdnotify.Stopping("Shutting down"); err != nil {
		a.Log.Debugf("sd_notify STOPPING failed: %v", err)
	}
	cancel()
	joined = group.drain(joined)
	if joined == nil {
		a.Log.Info("Service stopped gracefully")
		fmt.Println("service stopped gracefully")
	}
	return joined
}

func publishServiceReady(a *app.App) error {
	if _, err := config.Update(a.DB, func(cfg *types.Configuration) error {
		cfg.StartCounter++
		return nil
	}); err != nil {
		return fmt.Errorf("record service readiness: %w", err)
	}
	if err := sdnotify.Ready("Service ready"); err != nil {
		a.Log.Warnf("sd_notify READY failed: %v", err)
	}
	a.Log.Info("Service ready")
	return nil
}

func unexpectedComponentError(ctx context.Context, result componentResult) error {
	if result.err != nil {
		if errors.Is(result.err, context.Canceled) && ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("%s stopped: %w", result.name, result.err)
	}
	if ctx.Err() == nil {
		return fmt.Errorf("%s stopped unexpectedly", result.name)
	}
	return nil
}

// --- BEGIN service.https ---
func serviceRunConfiguration(a *app.App, portOverride int) (*types.Configuration, error) {
	cfg, err := config.View(a.DB)
	if err != nil {
		return nil, fmt.Errorf("failed to get service configuration: %w", err)
	}
	if portOverride != 0 {
		cfg.UIBind, err = bindWithPort(cfg.UIBind, portOverride, "")
		if err != nil {
			return nil, fmt.Errorf("failed to override service port: %w", err)
		}
	}
	return cfg, nil
}

// bindToBaseURL converts a listen bind like ":8484" or "0.0.0.0:8484" into a
// human-facing dashboard URL ("https://localhost:8484"). The dashboard always
// serves self-signed HTTPS on this bind.
func bindToBaseURL(bind string, defaultPort int) string {
	port := strconv.Itoa(defaultPort)
	if i := strings.LastIndex(bind, ":"); i >= 0 && i+1 < len(bind) {
		port = bind[i+1:]
	}
	return fmt.Sprintf("https://localhost:%s", port)
}

// --- END service.https ---
