// Package poller runs one independent polling loop per configured target,
// probing its protocol and reporting success to Healthchecks.io.
package poller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/miikkak/mc-conn-poller/internal/config"
	"github.com/miikkak/mc-conn-poller/internal/probe"
)

// Prober matches probe.Check's signature so tests can substitute a fake.
type Prober func(ctx context.Context, protocol, host string, port int, timeout time.Duration, family probe.IPFamily, opts probe.Options) error

// Notifier reports one successful probe to an external monitor at the given
// URL. httpPing and httpPushKuma match its signature, so tests can
// substitute fakes.
type Notifier func(ctx context.Context, url string, timeout time.Duration, report PingReport) error

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
	// Failure, when non-empty, marks this as a failed probe and holds the
	// (sanitized, truncated) reason. Only the Kuma notifier is ever given a
	// failure report; Latency is unset then.
	Failure string
}

// Run starts one polling goroutine per target and blocks until ctx is
// canceled.
func Run(ctx context.Context, cfg config.Config, logger *slog.Logger, info Info) error {
	return run(ctx, cfg, logger, info, probe.Check, httpPing, httpPushKuma)
}

func run(ctx context.Context, cfg config.Config, logger *slog.Logger, info Info, probeFn Prober, pingFn, kumaFn Notifier) error {
	var wg sync.WaitGroup
	for _, t := range cfg.Targets {
		wg.Add(1)
		go func(t config.Target) {
			defer wg.Done()
			pollTarget(ctx, t, logger, info, probeFn, pingFn, kumaFn)
		}(t)
	}
	wg.Wait()
	return nil
}

// familySelector returns the function that picks the IP family for each
// round of a target, per its ip_family setting. "ipv4"/"ipv6" pin every
// round to that family; anything else (including the default, empty) is
// "alternate", which alternates IPv4/IPv6 across rounds.
func familySelector(mode string) func() probe.IPFamily {
	switch mode {
	case config.IPFamilyIPv4:
		return func() probe.IPFamily { return probe.IPv4 }
	case config.IPFamilyIPv6:
		return func() probe.IPFamily { return probe.IPv6 }
	}
	round := 0
	return func() probe.IPFamily {
		family := probe.IPv4
		if round%2 == 1 {
			family = probe.IPv6
		}
		round++
		return family
	}
}

// pollTarget probes immediately on startup, then on t.Interval, until ctx is
// canceled. Each target runs on its own independent timer — per spec,
// timers across targets/hosts do not need to be synchronized or staggered.
//
// Each round pins the probe to a single IP address family: Go's default
// dialer races both and reports success if either connects, which would
// hide a real outage confined to one family. By default (ip_family
// "alternate") the family alternates IPv4/IPv6 across rounds, giving
// ongoing coverage of both without conflating them into a single result;
// note that this halves each family's ping rate, so a single-stack target
// (or poller) should set ip_family to that family instead. To alert on one
// family specifically, configure one pinned target per family, each with its
// own Healthchecks.io check.
func pollTarget(ctx context.Context, t config.Target, logger *slog.Logger, info Info, probeFn Prober, pingFn, kumaFn Notifier) {
	nextFamily := familySelector(t.IPFamily)

	probeOnce(ctx, t, logger, info, nextFamily(), probeFn, pingFn, kumaFn)

	ticker := time.NewTicker(t.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			probeOnce(ctx, t, logger, info, nextFamily(), probeFn, pingFn, kumaFn)
		}
	}
}

// notifierCall is one notifier to run for a round: its name for logs, the
// URL it reports to (empty means not configured, so it is skipped) and fn.
type notifierCall struct {
	name string
	url  string
	fn   Notifier
}

// notify runs the configured calls in parallel, each bounded by PingTimeout,
// so a second notifier doesn't lengthen the worst-case round that config
// validation accounts for. One failing never affects the other.
func notify(ctx context.Context, t config.Target, logger *slog.Logger, family probe.IPFamily, report PingReport, calls ...notifierCall) {
	var wg sync.WaitGroup
	for _, n := range calls {
		if n.url == "" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := n.fn(ctx, n.url, t.PingTimeout, report); err != nil {
				logger.Warn("notifier failed", "notifier", n.name, "target", t.Name, "ip_family", family, "error", err)
				return
			}
			logger.Debug("reported", "notifier", n.name, "target", t.Name, "ip_family", family, "failure", report.Failure != "")
		}()
	}
	wg.Wait()
}

// probeOnce reports a successful probe to the target's notifiers
// (Healthchecks.io, and Uptime Kuma if configured). On failure it never
// touches Healthchecks.io — no /fail call — since the check is shared across
// pollers on multiple hosts; an explicit fail from a poller with a locally
// broken path would flip a check other pollers are still successfully
// reporting on. Healthchecks.io's own grace-period silence model gives the
// correct "no poller anywhere succeeded" semantics instead.
//
// The one exception is opt-in per target (kuma_report_failure): a Kuma push
// monitor is normally per poller or per target, so a status=down push with
// the reason gives an immediate, explained alert rather than a silent
// heartbeat timeout. A probe that failed only because the daemon is shutting
// down is not a target failure and is never reported.
func probeOnce(ctx context.Context, t config.Target, logger *slog.Logger, info Info, family probe.IPFamily, probeFn Prober, pingFn, kumaFn Notifier) {
	report := PingReport{
		PollerInfo: info,
		Target:     t.Name,
		Protocol:   t.Protocol,
		Host:       t.Host,
		Port:       t.Port,
		IPFamily:   family,
	}

	start := time.Now()
	if err := probeFn(ctx, t.Protocol, t.Host, t.Port, t.Timeout, family, probe.Options{
		Transport:       t.Transport,
		SignalingScheme: t.SignalingScheme,
		Logger:          logger.With("target", t.Name),
	}); err != nil {
		if ctx.Err() != nil {
			return // shutting down: the probe was aborted, the target did not fail
		}
		if !t.KumaReportFailure {
			logger.Debug("probe failed, not reporting", "target", t.Name, "ip_family", family, "error", err)
			return
		}
		logger.Debug("probe failed, reporting to uptime kuma", "target", t.Name, "ip_family", family, "error", err)
		report.Failure = failureReason(err, t.PingURL, t.KumaPushURL)
		notify(ctx, t, logger, family, report, notifierCall{"uptime kuma", t.KumaPushURL, kumaFn})
		return
	}
	report.Latency = time.Since(start)

	notify(ctx, t, logger, family, report,
		notifierCall{"healthchecks.io", t.PingURL, pingFn},
		notifierCall{"uptime kuma", t.KumaPushURL, kumaFn},
	)
}

// maxFailureReason bounds the probe error text pushed to Kuma as msg.
const maxFailureReason = 200

// failureReason renders a probe error as a single-line, bounded message for
// Kuma's msg. Probe errors only carry the monitored target's own host:port,
// never a ping or push URL, but any configured notifier URL (a bearer
// secret) is redacted anyway so a future error path can't leak one.
func failureReason(err error, secrets ...string) string {
	msg := err.Error()
	for _, s := range secrets {
		if s != "" {
			msg = strings.ReplaceAll(msg, s, "[redacted]")
		}
	}
	msg = strings.Join(strings.Fields(msg), " ") // errors.Join output is multi-line
	if r := []rune(msg); len(r) > maxFailureReason {
		msg = string(r[:maxFailureReason]) + "…"
	}
	if msg == "" {
		msg = "probe failed"
	}
	return msg
}

const (
	// maxPingRedirects bounds how many redirects a ping follows; Healthchecks.io
	// itself never redirects, but a self-hosted instance may sit behind a
	// http-to-https redirect.
	maxPingRedirects = 3
	// maxPingBodyDrain bounds how much of the response body is read (and
	// discarded) to let the connection be reused.
	maxPingBodyDrain = 64 * 1024
)

// pingClient is dedicated to Healthchecks.io pings, rather than the shared
// http.DefaultClient, so a redirect limit can be applied without affecting
// anything else in the process. Per-request timeouts come from the request
// context.
var pingClient = &http.Client{
	CheckRedirect: func(_ *http.Request, via []*http.Request) error {
		if len(via) > maxPingRedirects {
			return fmt.Errorf("stopped after %d redirects", maxPingRedirects)
		}
		return nil
	},
}

// stripURL unwraps a *url.Error to its underlying error. A *url.Error's
// message embeds the full request URL, and a Healthchecks.io ping URL is a
// bearer secret (anyone holding it can mark the check as up), so it must not
// reach logs.
func stripURL(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
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
		return fmt.Errorf("build ping request: %w", stripURL(err))
	}
	req.Header.Set("User-Agent", userAgent(report.PollerInfo))
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")

	resp, err := pingClient.Do(req)
	if err != nil {
		return fmt.Errorf("send ping to %s: %w", req.URL.Host, stripURL(err))
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxPingBodyDrain)) // drain so the connection can be reused
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ping returned status %d", resp.StatusCode)
	}
	return nil
}

// httpPushKuma reports a probe to an Uptime Kuma push monitor: for a success
// GET <push URL>?status=up&msg=OK&ping=<latency ms>, for a failed probe
// (report.Failure set) ?status=down&msg=<reason>. These parameters override
// any already on the configured URL (Kuma's UI hands out a URL with
// "?status=up&msg=OK&ping=" appended); other query parameters are kept.
func httpPushKuma(ctx context.Context, pushURL string, timeout time.Duration, report PingReport) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	u, err := url.Parse(pushURL)
	if err != nil {
		return fmt.Errorf("build push request: %w", stripURL(err))
	}
	q := u.Query()
	if report.Failure != "" {
		q.Set("status", "down")
		q.Set("msg", report.Failure)
		q.Del("ping") // no latency for a failed probe
	} else {
		q.Set("status", "up")
		q.Set("msg", "OK")
		q.Set("ping", strconv.FormatInt(report.Latency.Milliseconds(), 10))
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("build push request: %w", stripURL(err))
	}
	req.Header.Set("User-Agent", userAgent(report.PollerInfo))

	resp, err := pingClient.Do(req)
	if err != nil {
		return fmt.Errorf("send push to %s: %w", req.URL.Host, stripURL(err))
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxPingBodyDrain)) // drain so the connection can be reused
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("push returned status %d", resp.StatusCode)
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
