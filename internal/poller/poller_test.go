package poller

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miikkak/mc-conn-poller/internal/config"
	"github.com/miikkak/mc-conn-poller/internal/probe"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

var testInfo = Info{Version: "test", Host: "test-host"}

func TestProbeOnceReportsOnlyOnProbeSuccess(t *testing.T) {
	target := config.Target{Name: "t", PingURL: "https://example.invalid/ping"}

	tests := []struct {
		name       string
		probeErr   error
		wantPinged bool
	}{
		{"probe succeeds", nil, true},
		{"probe fails", errors.New("unreachable"), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var pinged atomic.Bool
			probeFn := func(context.Context, string, string, int, time.Duration, probe.IPFamily) error { return tc.probeErr }
			pingFn := func(context.Context, string, time.Duration, PingReport) error {
				pinged.Store(true)
				return nil
			}

			probeOnce(context.Background(), target, discardLogger(), testInfo, probe.IPv4, probeFn, pingFn)

			if pinged.Load() != tc.wantPinged {
				t.Errorf("pinged = %v, want %v", pinged.Load(), tc.wantPinged)
			}
		})
	}
}

func TestProbeOnceSurvivesPingFailure(t *testing.T) {
	target := config.Target{Name: "t", PingURL: "https://example.invalid/ping"}
	probeFn := func(context.Context, string, string, int, time.Duration, probe.IPFamily) error { return nil }
	pingFn := func(context.Context, string, time.Duration, PingReport) error { return errors.New("network error") }

	// Must not panic; failure is logged and swallowed.
	probeOnce(context.Background(), target, discardLogger(), testInfo, probe.IPv4, probeFn, pingFn)
}

func TestProbeOnceReportsFamilyAndLatency(t *testing.T) {
	target := config.Target{Name: "t", Protocol: "java", Host: "mc.example.invalid", Port: 25565, PingURL: "https://example.invalid/ping"}

	var gotReport PingReport
	probeFn := func(context.Context, string, string, int, time.Duration, probe.IPFamily) error { return nil }
	pingFn := func(_ context.Context, _ string, _ time.Duration, report PingReport) error {
		gotReport = report
		return nil
	}

	probeOnce(context.Background(), target, discardLogger(), testInfo, probe.IPv6, probeFn, pingFn)

	if gotReport.IPFamily != probe.IPv6 {
		t.Errorf("report.IPFamily = %q, want %q", gotReport.IPFamily, probe.IPv6)
	}
	if gotReport.Target != "t" || gotReport.Protocol != "java" || gotReport.Host != "mc.example.invalid" || gotReport.Port != 25565 {
		t.Errorf("report = %+v, want target/protocol/host/port from target", gotReport)
	}
	if gotReport.PollerInfo != testInfo {
		t.Errorf("report.PollerInfo = %+v, want %+v", gotReport.PollerInfo, testInfo)
	}
}

// pollDeadline is a safety net only: the polling tests below wait for probe
// events rather than asserting on how many fit in a wall-clock window, so
// scheduling jitter on a loaded runner can slow them but not fail them. A
// test only hits this deadline if the poller genuinely stops probing.
const pollDeadline = 10 * time.Second

// pollUntil runs pollTarget on target until n probes have happened, then
// cancels it and returns the IP family of each of the first n probes.
func pollUntil(t *testing.T, target config.Target, n int) []probe.IPFamily {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), pollDeadline)
	defer cancel()

	var mu sync.Mutex
	var families []probe.IPFamily
	probeFn := func(_ context.Context, _ string, _ string, _ int, _ time.Duration, family probe.IPFamily) error {
		mu.Lock()
		defer mu.Unlock()
		families = append(families, family)
		if len(families) == n {
			cancel()
		}
		return nil
	}
	pingFn := func(context.Context, string, time.Duration, PingReport) error { return nil }

	pollTarget(ctx, target, discardLogger(), testInfo, probeFn, pingFn)

	mu.Lock()
	defer mu.Unlock()
	if len(families) < n {
		t.Fatalf("got %d probes before the %s deadline, want %d", len(families), pollDeadline, n)
	}
	// A tick can race the cancellation and add a probe past n; ignore it.
	return families[:n]
}

func TestPollTargetProbesImmediatelyThenOnInterval(t *testing.T) {
	t.Run("probes immediately at startup", func(t *testing.T) {
		// An hour-long interval means the only probe that can happen is the
		// immediate one; pollUntil would hit its deadline otherwise.
		target := config.Target{Name: "t", PingURL: "https://example.invalid/ping", Interval: time.Hour}
		pollUntil(t, target, 1)
	})

	t.Run("keeps probing on the interval", func(t *testing.T) {
		// The immediate probe plus two ticker-driven ones.
		target := config.Target{Name: "t", PingURL: "https://example.invalid/ping", Interval: time.Millisecond}
		pollUntil(t, target, 3)
	})
}

func TestPollTargetAlternatesIPFamilyAcrossRounds(t *testing.T) {
	target := config.Target{Name: "t", PingURL: "https://example.invalid/ping", Interval: time.Millisecond}

	families := pollUntil(t, target, 6)

	for i, f := range families {
		want := probe.IPv4
		if i%2 == 1 {
			want = probe.IPv6
		}
		if f != want {
			t.Errorf("families[%d] = %q, want %q (families = %v)", i, f, want, families)
		}
	}
}

func TestFamilySelector(t *testing.T) {
	tests := []struct {
		mode string
		want []probe.IPFamily
	}{
		{config.IPFamilyIPv4, []probe.IPFamily{probe.IPv4, probe.IPv4, probe.IPv4}},
		{config.IPFamilyIPv6, []probe.IPFamily{probe.IPv6, probe.IPv6, probe.IPv6}},
		{config.IPFamilyAlternate, []probe.IPFamily{probe.IPv4, probe.IPv6, probe.IPv4, probe.IPv6}},
		{"", []probe.IPFamily{probe.IPv4, probe.IPv6, probe.IPv4, probe.IPv6}},
	}
	for _, tc := range tests {
		t.Run("ip_family="+tc.mode, func(t *testing.T) {
			next := familySelector(tc.mode)
			for i, want := range tc.want {
				if got := next(); got != want {
					t.Errorf("round %d = %q, want %q", i, got, want)
				}
			}
		})
	}
}

func TestPollTargetPinsConfiguredIPFamily(t *testing.T) {
	target := config.Target{Name: "t", PingURL: "https://example.invalid/ping", Interval: time.Millisecond, IPFamily: config.IPFamilyIPv6}

	families := pollUntil(t, target, 4)

	for i, f := range families {
		if f != probe.IPv6 {
			t.Errorf("families[%d] = %q, want %q (families = %v)", i, f, probe.IPv6, families)
		}
	}
}

func TestRunPollsEveryTargetIndependently(t *testing.T) {
	// Hour-long intervals mean each target's only probe is the immediate
	// startup one, so "each host probed exactly once" is exactly "one
	// goroutine ran per target". Targets are told apart by host, which the
	// prober receives.
	cfg := config.Config{
		Targets: []config.Target{
			{Name: "a", Host: "a.invalid", PingURL: "https://example.invalid/a", Interval: time.Hour},
			{Name: "b", Host: "b.invalid", PingURL: "https://example.invalid/b", Interval: time.Hour},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), pollDeadline)
	defer cancel()

	var mu sync.Mutex
	probed := map[string]int{}
	probeFn := func(_ context.Context, _ string, host string, _ int, _ time.Duration, _ probe.IPFamily) error {
		mu.Lock()
		defer mu.Unlock()
		probed[host]++
		if len(probed) == len(cfg.Targets) {
			cancel() // every target has probed; stop the run
		}
		return nil
	}
	pingFn := func(context.Context, string, time.Duration, PingReport) error { return nil }

	if err := run(ctx, cfg, discardLogger(), testInfo, probeFn, pingFn); err != nil {
		t.Fatalf("run() = %v, want nil", err)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, host := range []string{"a.invalid", "b.invalid"} {
		if probed[host] != 1 {
			t.Errorf("probes of %s = %d, want 1 (probed = %v)", host, probed[host], probed)
		}
	}
}
