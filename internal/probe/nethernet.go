package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// maxSignalingResponse caps how much of a signaling reply is read, guarding
// against a misbehaving peer streaming an unbounded body.
const maxSignalingResponse = 1 << 20

// nethernetPing checks a Bedrock server's NetherNet signaling endpoint:
// GET /v1/join must answer 200 with a JSON object carrying a protocol
// number. Geyser serves the same document the Bedrock client reads before
// opening the WebRTC (UDP) leg, so a well-formed reply means the proxy is up
// and its signaling is reachable. The UDP gameplay leg is deliberately not
// exercised: it would need a full WebRTC handshake.
func nethernetPing(ctx context.Context, host string, port int, timeout time.Duration, family IPFamily, scheme string) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	u := url.URL{Scheme: scheme, Host: net.JoinHostPort(host, strconv.Itoa(port)), Path: "/v1/join"}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("nethernet signaling %s: %w", u.Host, err)
	}
	req.Header.Set("Accept", "application/json")

	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp"+string(family), addr)
		},
		DisableKeepAlives: true,
	}
	defer transport.CloseIdleConnections()

	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return fmt.Errorf("nethernet signaling %s (tcp%s): %w", u.Host, family, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("nethernet signaling %s (tcp%s): unexpected status %s", u.Host, family, resp.Status)
	}

	var status struct {
		Protocol *int `json:"protocol"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxSignalingResponse)).Decode(&status); err != nil {
		return fmt.Errorf("nethernet signaling %s (tcp%s): malformed response: %w", u.Host, family, err)
	}
	if status.Protocol == nil {
		return fmt.Errorf("nethernet signaling %s (tcp%s): response has no protocol field", u.Host, family)
	}
	return nil
}
