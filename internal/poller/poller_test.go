package poller

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miikkak/mc-conn-poller/internal/config"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

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
			probeFn := func(string, string, int, time.Duration) error { return tc.probeErr }
			pingFn := func(context.Context, string, time.Duration) error {
				pinged.Store(true)
				return nil
			}

			probeOnce(context.Background(), target, discardLogger(), probeFn, pingFn)

			if pinged.Load() != tc.wantPinged {
				t.Errorf("pinged = %v, want %v", pinged.Load(), tc.wantPinged)
			}
		})
	}
}

func TestProbeOnceSurvivesPingFailure(t *testing.T) {
	target := config.Target{Name: "t", PingURL: "https://example.invalid/ping"}
	probeFn := func(string, string, int, time.Duration) error { return nil }
	pingFn := func(context.Context, string, time.Duration) error { return errors.New("network error") }

	// Must not panic; failure is logged and swallowed.
	probeOnce(context.Background(), target, discardLogger(), probeFn, pingFn)
}

func TestPollTargetProbesImmediatelyThenOnInterval(t *testing.T) {
	target := config.Target{Name: "t", PingURL: "https://example.invalid/ping", Interval: 10 * time.Millisecond}

	var probeCount atomic.Int32
	probeFn := func(string, string, int, time.Duration) error {
		probeCount.Add(1)
		return nil
	}
	pingFn := func(context.Context, string, time.Duration) error { return nil }

	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()

	pollTarget(ctx, target, discardLogger(), probeFn, pingFn)

	// One immediate probe at startup, plus at least two more from the
	// ticker in the ~35ms window before ctx is canceled.
	if got := probeCount.Load(); got < 3 {
		t.Errorf("probeCount = %d, want at least 3", got)
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
	probeFn := func(string, string, int, time.Duration) error {
		probeCount.Add(1)
		return nil
	}
	pingFn := func(context.Context, string, time.Duration) error { return nil }

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	if err := run(ctx, cfg, discardLogger(), probeFn, pingFn); err != nil {
		t.Fatalf("run() = %v, want nil", err)
	}

	if got := probeCount.Load(); got != 2 {
		t.Errorf("probeCount = %d, want 2 (one per target)", got)
	}
}
