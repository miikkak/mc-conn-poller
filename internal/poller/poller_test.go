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
			probeFn := func(string, string, int, time.Duration, probe.IPFamily) error { return tc.probeErr }
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
	probeFn := func(string, string, int, time.Duration, probe.IPFamily) error { return nil }
	pingFn := func(context.Context, string, time.Duration, PingReport) error { return errors.New("network error") }

	// Must not panic; failure is logged and swallowed.
	probeOnce(context.Background(), target, discardLogger(), testInfo, probe.IPv4, probeFn, pingFn)
}

func TestProbeOnceReportsFamilyAndLatency(t *testing.T) {
	target := config.Target{Name: "t", Protocol: "java", Host: "mc.example.invalid", Port: 25565, PingURL: "https://example.invalid/ping"}

	var gotReport PingReport
	probeFn := func(string, string, int, time.Duration, probe.IPFamily) error { return nil }
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

func TestPollTargetProbesImmediatelyThenOnInterval(t *testing.T) {
	target := config.Target{Name: "t", PingURL: "https://example.invalid/ping", Interval: 10 * time.Millisecond}

	var probeCount atomic.Int32
	probeFn := func(string, string, int, time.Duration, probe.IPFamily) error {
		probeCount.Add(1)
		return nil
	}
	pingFn := func(context.Context, string, time.Duration, PingReport) error { return nil }

	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()

	pollTarget(ctx, target, discardLogger(), testInfo, probeFn, pingFn)

	// One immediate probe at startup, plus at least two more from the
	// ticker in the ~35ms window before ctx is canceled.
	if got := probeCount.Load(); got < 3 {
		t.Errorf("probeCount = %d, want at least 3", got)
	}
}

func TestPollTargetAlternatesIPFamilyAcrossRounds(t *testing.T) {
	target := config.Target{Name: "t", PingURL: "https://example.invalid/ping", Interval: 5 * time.Millisecond}

	var mu sync.Mutex
	var families []probe.IPFamily
	probeFn := func(_ string, _ string, _ int, _ time.Duration, family probe.IPFamily) error {
		mu.Lock()
		families = append(families, family)
		mu.Unlock()
		return nil
	}
	pingFn := func(context.Context, string, time.Duration, PingReport) error { return nil }

	ctx, cancel := context.WithTimeout(context.Background(), 27*time.Millisecond)
	defer cancel()

	pollTarget(ctx, target, discardLogger(), testInfo, probeFn, pingFn)

	mu.Lock()
	defer mu.Unlock()
	if len(families) < 4 {
		t.Fatalf("got %d probes, want at least 4 to observe alternation", len(families))
	}
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

func TestRunPollsEveryTargetIndependently(t *testing.T) {
	// Hour-long intervals mean each target's only probe within the test
	// window is the immediate startup one — so the count below is exactly
	// "one goroutine ran per target", not an artifact of ticker timing.
	cfg := config.Config{
		Targets: []config.Target{
			{Name: "a", PingURL: "https://example.invalid/a", Interval: time.Hour},
			{Name: "b", PingURL: "https://example.invalid/b", Interval: time.Hour},
		},
	}

	var probeCount atomic.Int32
	probeFn := func(string, string, int, time.Duration, probe.IPFamily) error {
		probeCount.Add(1)
		return nil
	}
	pingFn := func(context.Context, string, time.Duration, PingReport) error { return nil }

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	if err := run(ctx, cfg, discardLogger(), testInfo, probeFn, pingFn); err != nil {
		t.Fatalf("run() = %v, want nil", err)
	}

	if got := probeCount.Load(); got != 2 {
		t.Errorf("probeCount = %d, want 2 (one per target)", got)
	}
}
