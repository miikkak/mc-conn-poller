// Package poller runs one independent polling loop per configured target,
// probing its protocol and reporting success to Healthchecks.io.
package poller

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/miikkak/mc-conn-poller/internal/config"
	"github.com/miikkak/mc-conn-poller/internal/probe"
)

// Prober matches probe.Check's signature so tests can substitute a fake.
type Prober func(protocol, host string, port int, timeout time.Duration) error

// Pinger matches httpPing's signature so tests can substitute a fake.
type Pinger func(ctx context.Context, pingURL string, timeout time.Duration) error

// Run starts one polling goroutine per target and blocks until ctx is
// canceled.
func Run(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	return run(ctx, cfg, logger, probe.Check, httpPing)
}

func run(ctx context.Context, cfg config.Config, logger *slog.Logger, probeFn Prober, pingFn Pinger) error {
	var wg sync.WaitGroup
	for _, t := range cfg.Targets {
		wg.Add(1)
		go func(t config.Target) {
			defer wg.Done()
			pollTarget(ctx, t, logger, probeFn, pingFn)
		}(t)
	}
	wg.Wait()
	return nil
}

// pollTarget probes immediately on startup, then on t.Interval, until ctx is
// canceled. Each target runs on its own independent timer — per spec,
// timers across targets/hosts do not need to be synchronized or staggered.
func pollTarget(ctx context.Context, t config.Target, logger *slog.Logger, probeFn Prober, pingFn Pinger) {
	probeOnce(ctx, t, logger, probeFn, pingFn)

	ticker := time.NewTicker(t.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			probeOnce(ctx, t, logger, probeFn, pingFn)
		}
	}
}

// probeOnce reports to Healthchecks.io only on a successful probe. On
// failure it deliberately does nothing — no /fail call — since the check is
// shared across pollers on multiple hosts; an explicit fail from a poller
// with a locally broken path would flip a check other pollers are still
// successfully reporting on. Healthchecks.io's own grace-period silence
// model gives the correct "no poller anywhere succeeded" semantics instead.
func probeOnce(ctx context.Context, t config.Target, logger *slog.Logger, probeFn Prober, pingFn Pinger) {
	if err := probeFn(t.Protocol, t.Host, t.Port, t.Timeout); err != nil {
		logger.Debug("probe failed, not reporting", "target", t.Name, "error", err)
		return
	}
	if err := pingFn(ctx, t.PingURL, t.Timeout); err != nil {
		logger.Warn("healthchecks.io ping failed", "target", t.Name, "error", err)
		return
	}
	logger.Debug("probe succeeded, reported to healthchecks.io", "target", t.Name)
}

func httpPing(ctx context.Context, pingURL string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pingURL, nil)
	if err != nil {
		return fmt.Errorf("build ping request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("send ping: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ping returned status %d", resp.StatusCode)
	}
	return nil
}
