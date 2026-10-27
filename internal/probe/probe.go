// Package probe dispatches a protocol-level connectivity check to the
// right client for a target's protocol.
package probe

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"time"

	"github.com/miikkak/mc-conn-poller/internal/slp"
	"github.com/sandertv/go-raknet"
)

// IPFamily pins a probe to a single IP address family. Go's default dialer
// races IPv4 and IPv6 (Happy Eyeballs) and reports success if either
// connects, which can hide a real outage on one family behind a working
// leg on the other. Pinning the family per probe avoids that, and the
// poller alternates families across rounds so both get exercised over
// time.
type IPFamily string

const (
	IPv4 IPFamily = "4"
	IPv6 IPFamily = "6"
)

// Bedrock transports. "auto" tries NetherNet first and falls back to RakNet.
const (
	TransportAuto      = "auto"
	TransportNetherNet = "nethernet"
	TransportRakNet    = "raknet"
)

// Options tunes the Bedrock probe; the zero value means TransportAuto with
// HTTPS signaling. It has no effect on "java" probes.
type Options struct {
	// Transport is TransportAuto (default when empty), TransportNetherNet or
	// TransportRakNet.
	Transport string
	// SignalingScheme is "https" (default when empty) or "http", for the
	// NetherNet signaling endpoint.
	SignalingScheme string
	// Logger, if set, reports a successful RakNet fallback in auto mode so
	// it doesn't quietly mask a broken NetherNet path.
	Logger *slog.Logger
}

// Check performs a protocol-level handshake against host:port over the
// given IP family and returns nil only when a genuine protocol response
// comes back within timeout. For "java", slp.Status still requires a
// well-formed status packet (correct packet ID, valid JSON) — a server
// that responds but with a broken payload is reported as unreachable, not
// treated as connectivity success. Canceling ctx aborts an in-flight probe
// immediately rather than waiting out timeout. For "bedrock", opts selects
// the transport (see Options); in auto mode timeout applies to each attempt,
// so a NetherNet timeout followed by a RakNet fallback can take up to twice
// timeout. That is deliberate: a shared budget would leave the fallback no
// time after a silently dropped NetherNet attempt.
func Check(ctx context.Context, protocol, host string, port int, timeout time.Duration, family IPFamily, opts Options) error {
	switch protocol {
	case "java":
		return slp.Status(ctx, host, port, timeout, "tcp"+string(family))
	case "bedrock":
		return bedrockCheck(ctx, host, port, timeout, family, opts)
	default:
		return fmt.Errorf("unknown protocol %q", protocol)
	}
}

func bedrockCheck(ctx context.Context, host string, port int, timeout time.Duration, family IPFamily, opts Options) error {
	scheme := opts.SignalingScheme
	if scheme == "" {
		scheme = "https"
	}

	switch opts.Transport {
	case TransportNetherNet:
		return nethernetPing(ctx, host, port, timeout, family, scheme)
	case TransportRakNet:
		return bedrockPing(ctx, host, port, timeout, family)
	case "", TransportAuto:
		nnErr := nethernetPing(ctx, host, port, timeout, family, scheme)
		if nnErr == nil {
			return nil
		}
		if ctx.Err() != nil {
			return nnErr
		}
		rnErr := bedrockPing(ctx, host, port, timeout, family)
		if rnErr != nil {
			return errors.Join(nnErr, rnErr)
		}
		if opts.Logger != nil {
			opts.Logger.Warn("nethernet probe failed, raknet fallback succeeded", "host", host, "port", port, "error", nnErr)
		}
		return nil
	default:
		return fmt.Errorf("unknown bedrock transport %q", opts.Transport)
	}
}

func bedrockPing(ctx context.Context, host string, port int, timeout time.Duration, family IPFamily) error {
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dialer := raknet.Dialer{UpstreamDialer: familyDialer{network: "udp" + string(family)}}
	response, err := dialer.PingContext(ctx, addr)
	if err != nil {
		return fmt.Errorf("bedrock ping %s (udp%s): %w", addr, family, err)
	}
	if len(response) == 0 {
		return fmt.Errorf("bedrock ping %s (udp%s): empty response", addr, family)
	}
	return nil
}

// familyDialer implements raknet.UpstreamDialer, forcing the outgoing
// connection onto a specific IP address family. raknet always requests the
// unqualified "udp" network itself, so the family it would otherwise pick
// is overridden here rather than passed through.
type familyDialer struct {
	network string
}

func (d familyDialer) DialContext(ctx context.Context, _, address string) (net.Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, d.network, address)
	if err != nil {
		return nil, err
	}
	return closeOnCancel(ctx, conn), nil
}

// cancelConn closes its connection when ctx is canceled. go-raknet applies
// only ctx's deadline to the socket and never watches ctx.Done(), so without
// this a blocked read would hold up shutdown for the full probe timeout
// (the Java path does the same with context.AfterFunc in slp.Query).
// Deadline expiry is deliberately left to the socket deadline, so timeouts
// keep reporting as i/o timeouts rather than as a closed connection.
type cancelConn struct {
	net.Conn
	stop func() bool
}

func closeOnCancel(ctx context.Context, conn net.Conn) net.Conn {
	c := &cancelConn{Conn: conn}
	c.stop = context.AfterFunc(ctx, func() {
		if ctx.Err() == context.Canceled {
			_ = conn.Close()
		}
	})
	return c
}

func (c *cancelConn) Close() error {
	c.stop()
	return c.Conn.Close()
}
