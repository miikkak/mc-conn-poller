package slp

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"strconv"
	"testing"
	"time"
)

// fakeServer accepts a single connection, reads the handshake + status
// request packets (without validating their contents), then writes the
// given raw response bytes verbatim.
func fakeServer(t *testing.T, response []byte) (host string, port int) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		r := bufio.NewReader(conn)
		if _, err := readPacket(r); err != nil { // handshake
			return
		}
		if _, err := readPacket(r); err != nil { // status request
			return
		}
		_, _ = conn.Write(response)
	}()

	host, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	port, err = strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return host, port
}

func validStatusResponse(json string) []byte {
	var packet []byte
	packet = append(packet, varInt(0x00)...)
	packet = append(packet, packString(json)...)

	var full []byte
	full = append(full, varInt(int32(len(packet)))...)
	full = append(full, packet...)
	return full
}

// stallingServer accepts a single connection, reads the handshake + status
// request, writes partial verbatim, closes sent, and then stalls without
// sending or closing anything until the test ends. A client reading its
// response is therefore blocked until it gives up or is canceled.
func stallingServer(t *testing.T, partial []byte) (host string, port int, sent <-chan struct{}) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan struct{})
	t.Cleanup(func() {
		close(done)
		_ = ln.Close()
	})

	sentCh := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		r := bufio.NewReader(conn)
		if _, err := readPacket(r); err != nil { // handshake
			return
		}
		if _, err := readPacket(r); err != nil { // status request
			return
		}
		_, _ = conn.Write(partial)
		close(sentCh)
		<-done
	}()

	host, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	port, err = strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return host, port, sentCh
}

func TestQuery_CancellationUnblocksReadPromptly(t *testing.T) {
	full := validStatusResponse(`{"version":{"name":"x","protocol":1}}`)

	tests := []struct {
		name    string
		partial []byte
	}{
		{"before any response byte", nil},
		{"partway through the response", full[:len(full)-3]},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			host, port, sent := stallingServer(t, tc.partial)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Cancel only once the server has sent everything it will send,
			// so the client is (or is about to be) blocked reading the rest.
			go func() {
				<-sent
				cancel()
			}()

			const timeout = 10 * time.Second
			start := time.Now()
			_, err := Query(ctx, host, port, timeout, "tcp")
			elapsed := time.Since(start)

			if err == nil {
				t.Fatal("Query() = nil error, want an error for a canceled query")
			}
			// Without the cancellation hook the read would wait out the full
			// timeout; returning in under half of it is a wide margin over
			// the milliseconds it actually takes.
			if elapsed > timeout/2 {
				t.Errorf("Query() took %s after cancellation, want it to return promptly (timeout %s)", elapsed, timeout)
			}
		})
	}
}

func TestStatus_ValidResponse(t *testing.T) {
	host, port := fakeServer(t, validStatusResponse(`{"version":{"name":"1.21"}}`))

	if err := Status(context.Background(), host, port, time.Second, "tcp"); err != nil {
		t.Fatalf("Status() = %v, want nil", err)
	}
}

func TestQuery_ValidResponse(t *testing.T) {
	host, port := fakeServer(t, validStatusResponse(`{"players":{"online":3,"max":20}}`))

	payload, err := Query(context.Background(), host, port, time.Second, "tcp")
	if err != nil {
		t.Fatalf("Query() = %v, want nil", err)
	}
	players, ok := payload["players"].(map[string]any)
	if !ok {
		t.Fatalf("payload[\"players\"] = %v, want map[string]any", payload["players"])
	}
	if online, want := players["online"], float64(3); online != want {
		t.Fatalf("players.online = %v, want %v", online, want)
	}
	if max, want := players["max"], float64(20); max != want {
		t.Fatalf("players.max = %v, want %v", max, want)
	}
}

func TestQuery_MalformedJSON(t *testing.T) {
	host, port := fakeServer(t, validStatusResponse(`not json`))

	if _, err := Query(context.Background(), host, port, time.Second, "tcp"); err == nil {
		t.Fatal("Query() = nil, want error for malformed JSON")
	}
}

func TestStatus_MalformedJSON(t *testing.T) {
	host, port := fakeServer(t, validStatusResponse(`not json`))

	if err := Status(context.Background(), host, port, time.Second, "tcp"); err == nil {
		t.Fatal("Status() = nil, want error for malformed JSON")
	}
}

func TestStatus_WrongPacketID(t *testing.T) {
	packet := append(varInt(0x01), packString(`{}`)...)
	var full []byte
	full = append(full, varInt(int32(len(packet)))...)
	full = append(full, packet...)

	host, port := fakeServer(t, full)

	if err := Status(context.Background(), host, port, time.Second, "tcp"); err == nil {
		t.Fatal("Status() = nil, want error for wrong packet id")
	}
}

func TestStatus_OversizedJSONLength(t *testing.T) {
	packet := append(varInt(0x00), varInt(1<<20)...) // claims a 1 MiB string, no body follows
	var full []byte
	full = append(full, varInt(int32(len(packet)))...)
	full = append(full, packet...)

	host, port := fakeServer(t, full)

	if err := Status(context.Background(), host, port, time.Second, "tcp"); err == nil {
		t.Fatal("Status() = nil, want error for oversized json length")
	}
}

func TestStatus_ConnectionRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	_ = ln.Close() // free the port immediately so nothing listens on it

	if err := Status(context.Background(), host, port, 500*time.Millisecond, "tcp"); err == nil {
		t.Fatal("Status() = nil, want error for closed port")
	}
}

func TestVarIntRoundTrip(t *testing.T) {
	cases := []int32{0, 1, 127, 128, 255, 25565, 2097151, 1 << 30}

	for _, n := range cases {
		encoded := varInt(n)
		got, err := readVarInt(bufio.NewReader(bytes.NewReader(encoded)))
		if err != nil {
			t.Fatalf("readVarInt(%d): %v", n, err)
		}
		if got != n {
			t.Fatalf("readVarInt roundtrip = %d, want %d", got, n)
		}
	}
}

func TestReadVarIntRejectsInt32Overflow(t *testing.T) {
	// 5 bytes, continuation set on the first 4; the 5th byte's upper nibble
	// (0xF0) is non-zero, which can't happen for a value that fits in int32.
	encoded := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0x1F}
	if _, err := readVarInt(bufio.NewReader(bytes.NewReader(encoded))); err == nil {
		t.Fatal("readVarInt() = nil error, want error for overflowing varint")
	}
}
