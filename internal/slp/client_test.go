package slp

import (
	"bufio"
	"bytes"
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

func TestStatus_ValidResponse(t *testing.T) {
	host, port := fakeServer(t, validStatusResponse(`{"version":{"name":"1.21"}}`))

	if err := Status(host, port, time.Second); err != nil {
		t.Fatalf("Status() = %v, want nil", err)
	}
}

func TestQuery_ValidResponse(t *testing.T) {
	host, port := fakeServer(t, validStatusResponse(`{"players":{"online":3,"max":20}}`))

	payload, err := Query(host, port, time.Second)
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

	if _, err := Query(host, port, time.Second); err == nil {
		t.Fatal("Query() = nil, want error for malformed JSON")
	}
}

func TestStatus_MalformedJSON(t *testing.T) {
	host, port := fakeServer(t, validStatusResponse(`not json`))

	if err := Status(host, port, time.Second); err == nil {
		t.Fatal("Status() = nil, want error for malformed JSON")
	}
}

func TestStatus_WrongPacketID(t *testing.T) {
	packet := append(varInt(0x01), packString(`{}`)...)
	var full []byte
	full = append(full, varInt(int32(len(packet)))...)
	full = append(full, packet...)

	host, port := fakeServer(t, full)

	if err := Status(host, port, time.Second); err == nil {
		t.Fatal("Status() = nil, want error for wrong packet id")
	}
}

func TestStatus_OversizedJSONLength(t *testing.T) {
	packet := append(varInt(0x00), varInt(1<<20)...) // claims a 1 MiB string, no body follows
	var full []byte
	full = append(full, varInt(int32(len(packet)))...)
	full = append(full, packet...)

	host, port := fakeServer(t, full)

	if err := Status(host, port, time.Second); err == nil {
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

	if err := Status(host, port, 500*time.Millisecond); err == nil {
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
