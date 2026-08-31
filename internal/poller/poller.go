// Package poller runs one independent polling loop per configured target,
// probing its protocol and reporting success to Healthchecks.io.
package poller

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/miikkak/mc-conn-poller/internal/config"
	"github.com/miikkak/mc-conn-poller/internal/probe"
)

// Prober matches probe.Check's signature so tests can substitute a fake.
type Prober func(protocol, host string, port int, timeout time.Duration, family probe.IPFamily) error

// Pinger matches httpPing's signature so tests can substitute a fake.
type Pinger func(ctx context.Context, pingURL string, timeout time.Duration, report PingReport) error

// Info identifies this daemon process/host, reported to Healthchecks.io as
// diagnostic context (User-Agent and ping body) alongside each successful
// probe.
type Info struct {
	// Version is the daemon build version (main.version, "dev" if unset).
	Version string
	// Host is this poller's own hostname, distinguishing which of
	// potentially several poller hosts reported a given ping.
	Host string
}

// PingReport carries diagnostic context about a successful probe, reported
// to Healthchecks.io as the ping's body.
type PingReport struct {
	PollerInfo Info
	Target     string
	Protocol   string
	Host       string
	Port       int
	IPFamily   probe.IPFamily
	Latency    time.Duration
}

// Run starts one polling goroutine per target and blocks until ctx is
// canceled.
func Run(ctx context.Context, cfg config.Config, logger *slog.Logger, info Info) error {
	return run(ctx, cfg, logger, info, probe.Check, httpPing)
}

func run(ctx context.Context, cfg config.Config, logger *slog.Logger, info Info, probeFn Prober, pingFn Pinger) error {
	var wg sync.WaitGroup
	for _, t := range cfg.Targets {
		wg.Add(1)
		go func(t config.Target) {
			defer wg.Done()
			pollTarget(ctx, t, logger, info, probeFn, pingFn)
		}(t)
	}
	wg.Wait()
	return nil
}

// pollTarget probes immediately on startup, then on t.Interval, until ctx is
// canceled. Each target runs on its own independent timer — per spec,
// timers across targets/hosts do not need to be synchronized or staggered.
//
// Each round pins the probe to a single IP address family, alternating
// IPv4/IPv6 across rounds: Go's default dialer races both and reports
// success if either connects, which would hide a real outage confined to
// one family. Alternating instead of always picking one gives ongoing
// coverage of both without conflating them into a single result.
func pollTarget(ctx context.Context, t config.Target, logger *slog.Logger, info Info, probeFn Prober, pingFn Pinger) {
	round := 0
	nextFamily := func() probe.IPFamily {
		family := probe.IPv4
		if round%2 == 1 {
			family = probe.IPv6
		}
		round++
		return family
	}

	probeOnce(ctx, t, logger, info, nextFamily(), probeFn, pingFn)

	ticker := time.NewTicker(t.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			probeOnce(ctx, t, logger, info, nextFamily(), probeFn, pingFn)
		}
	}
}

// probeOnce reports to Healthchecks.io only on a successful probe. On
// failure it deliberately does nothing — no /fail call — since the check is
// shared across pollers on multiple hosts; an explicit fail from a poller
// with a locally broken path would flip a check other pollers are still
// successfully reporting on. Healthchecks.io's own grace-period silence
// model gives the correct "no poller anywhere succeeded" semantics instead.
func probeOnce(ctx context.Context, t config.Target, logger *slog.Logger, info Info, family probe.IPFamily, probeFn Prober, pingFn Pinger) {
	start := time.Now()
	if err := probeFn(t.Protocol, t.Host, t.Port, t.Timeout, family); err != nil {
		logger.Debug("probe failed, not reporting", "target", t.Name, "ip_family", family, "error", err)
		return
	}
	latency := time.Since(start)

	report := PingReport{
		PollerInfo: info,
		Target:     t.Name,
		Protocol:   t.Protocol,
		Host:       t.Host,
		Port:       t.Port,
		IPFamily:   family,
		Latency:    latency,
	}
	if err := pingFn(ctx, t.PingURL, t.Timeout, report); err != nil {
		logger.Warn("healthchecks.io ping failed", "target", t.Name, "ip_family", family, "error", err)
		return
	}
	logger.Debug("probe succeeded, reported to healthchecks.io", "target", t.Name, "ip_family", family)
}

// httpPing reports a successful probe to Healthchecks.io. It deliberately
// leaves the connection's own IP family selection to the OS default (Go's
// Happy Eyeballs) rather than pinning it the way probeFn is pinned: unlike
// the Minecraft probe, there's no need for the ping itself to represent a
// specific family, and doing so would just forgo the OS's own dual-stack
// handling (racing, source address selection, NAT64/464XLAT, etc).
func httpPing(ctx context.Context, pingURL string, timeout time.Duration, report PingReport) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pingURL, strings.NewReader(reportBody(report)))
	if err != nil {
		return fmt.Errorf("build ping request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent(report.PollerInfo))
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")

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

func userAgent(info Info) string {
	version := info.Version
	if version == "" {
		version = "dev"
	}
	host := info.Host
	if host == "" {
		host = "unknown"
	}
	return fmt.Sprintf("mc-conn-poller/%s (%s)", version, host)
}

// reportBody renders a PingReport as Healthchecks.io's ping body: plain
// text, logged verbatim against the ping for later inspection.
func reportBody(r PingReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "poller_host=%s\n", nonEmpty(r.PollerInfo.Host))
	fmt.Fprintf(&b, "poller_version=%s\n", nonEmpty(r.PollerInfo.Version))
	fmt.Fprintf(&b, "target=%s\n", r.Target)
	fmt.Fprintf(&b, "protocol=%s\n", r.Protocol)
	fmt.Fprintf(&b, "host=%s\n", r.Host)
	fmt.Fprintf(&b, "port=%d\n", r.Port)
	fmt.Fprintf(&b, "ip_family=%s\n", r.IPFamily)
	fmt.Fprintf(&b, "probe_latency_ms=%d\n", r.Latency.Milliseconds())
	return b.String()
}

func nonEmpty(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
