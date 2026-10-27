package probe

import (
	"context"
	"encoding/json"
	"errors"
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

	// Redirects are not followed: a 3xx from the configured endpoint must
	// not let another server's 200 pass for this target.
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
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
	dec := json.NewDecoder(io.LimitReader(resp.Body, maxSignalingResponse))
	if err := dec.Decode(&status); err != nil {
		return fmt.Errorf("nethernet signaling %s (tcp%s): malformed response: %w", u.Host, family, err)
	}
	// Exactly one JSON document: anything but trailing whitespace is malformed.
	if err := dec.Decode(&json.RawMessage{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("nethernet signaling %s (tcp%s): malformed response: trailing data after JSON document", u.Host, family)
	}
	if status.Protocol == nil {
		return fmt.Errorf("nethernet signaling %s (tcp%s): response has no protocol field", u.Host, family)
	}
	return nil
}
