// Package probe dispatches a protocol-level connectivity check to the
// right client for a target's protocol.
package probe

import (
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/miikkak/mc-conn-poller/internal/slp"
	"github.com/sandertv/go-raknet"
)

// Check performs a protocol-level handshake against host:port and returns
// nil only when a genuine protocol response comes back within timeout. For
// "java", slp.Status still requires a well-formed status packet (correct
// packet ID, valid JSON) — a server that responds but with a broken payload
// is reported as unreachable, not treated as connectivity success.
func Check(protocol, host string, port int, timeout time.Duration) error {
	switch protocol {
	case "java":
		return slp.Status(host, port, timeout)
	case "bedrock":
		return bedrockPing(host, port, timeout)
	default:
		return fmt.Errorf("unknown protocol %q", protocol)
	}
}

func bedrockPing(host string, port int, timeout time.Duration) error {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	response, err := raknet.PingTimeout(addr, timeout)
	if err != nil {
		return fmt.Errorf("bedrock ping %s: %w", addr, err)
	}
	if len(response) == 0 {
		return fmt.Errorf("bedrock ping %s: empty response", addr)
	}
	return nil
}
