package probe

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"
)

// silentUDPListener binds a local UDP socket that never answers, so a
// Bedrock ping against it can only end by timeout or cancellation.
func silentUDPListener(t *testing.T) (host string, port int) {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = pc.Close() })

	h, p, err := net.SplitHostPort(pc.LocalAddr().String())
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	port, err = strconv.Atoi(p)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return h, port
}

func TestBedrockPingReturnsPromptlyOnCancellation(t *testing.T) {
	host, port := silentUDPListener(t)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)

	const timeout = 5 * time.Second
	start := time.Now()
	err := Check(ctx, "bedrock", host, port, timeout, IPv4)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Check() = nil, want error for a canceled probe")
	}
	if elapsed > timeout/2 {
		t.Errorf("Check() took %s after cancellation, want it to return promptly (timeout %s)", elapsed, timeout)
	}
}

func TestBedrockPingTimesOutWithoutResponse(t *testing.T) {
	host, port := silentUDPListener(t)

	start := time.Now()
	err := Check(context.Background(), "bedrock", host, port, 200*time.Millisecond, IPv4)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Check() = nil, want a timeout error")
	}
	if elapsed > 2*time.Second {
		t.Errorf("Check() took %s, want roughly the 200ms timeout", elapsed)
	}
}

func TestCheckRejectsUnknownProtocol(t *testing.T) {
	if err := Check(context.Background(), "gopher", "127.0.0.1", 1, time.Second, IPv4); err == nil {
		t.Error("Check() = nil, want error for unknown protocol")
	}
}
