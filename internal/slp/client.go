// Package slp implements a minimal Minecraft Java Edition Server List Ping
// client: just enough of the handshake + status protocol to confirm a server
// is alive and returning well-formed status JSON.
//
// Duplicated from github.com/miikkak/mc-healthcheck's internal/slp rather
// than imported: mc-healthcheck is a private repo, and GitHub Actions'
// default GITHUB_TOKEN can't fetch a different private repo's module during
// this repo's CI, so a real cross-repo dependency would need a PAT threaded
// through every workflow that builds Go code. The protocol is stable enough
// that keeping two copies in sync is the cheaper tradeoff. Apply any fix
// made here to the other copy too, and vice versa.
package slp

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

const maxResponseLength = 1 << 20 // guards against a misbehaving peer claiming an absurd length

// Status performs a Server List Ping against host:port over the given
// network ("tcp", "tcp4", or "tcp6") and returns an error unless a
// well-formed JSON status response is received within timeout.
func Status(host string, port int, timeout time.Duration, network string) error {
	_, err := Query(host, port, timeout, network)
	return err
}

// Query performs a Server List Ping against host:port over the given
// network ("tcp", "tcp4", or "tcp6") and returns the decoded status
// response (version, players, MOTD, etc.) within timeout.
func Query(host string, port int, timeout time.Duration, network string) (map[string]any, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	deadline := time.Now().Add(timeout)

	conn, err := net.DialTimeout(network, addr, timeout)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()

	if err := conn.SetDeadline(deadline); err != nil {
		return nil, fmt.Errorf("set deadline: %w", err)
	}

	if err := writeHandshake(conn, host, port); err != nil {
		return nil, fmt.Errorf("write handshake: %w", err)
	}
	if err := writePacket(conn, varInt(0x00)); err != nil { // empty status-request packet
		return nil, fmt.Errorf("write status request: %w", err)
	}

	body, err := readPacket(bufio.NewReader(conn))
	if err != nil {
		return nil, fmt.Errorf("read status response: %w", err)
	}

	br := bytes.NewReader(body)
	packetID, err := readVarInt(br)
	if err != nil {
		return nil, fmt.Errorf("read response packet id: %w", err)
	}
	if packetID != 0x00 {
		return nil, fmt.Errorf("unexpected response packet id: %d", packetID)
	}

	jsonLen, err := readVarInt(br)
	if err != nil {
		return nil, fmt.Errorf("read json length: %w", err)
	}
	if jsonLen < 0 || jsonLen > int32(br.Len()) {
		return nil, fmt.Errorf("invalid json length: %d", jsonLen)
	}
	jsonBytes := make([]byte, jsonLen)
	if _, err := io.ReadFull(br, jsonBytes); err != nil {
		return nil, fmt.Errorf("read json body: %w", err)
	}
	if br.Len() != 0 {
		return nil, fmt.Errorf("unexpected trailing data after json body: %d bytes", br.Len())
	}

	var payload map[string]any
	if err := json.Unmarshal(jsonBytes, &payload); err != nil {
		return nil, fmt.Errorf("invalid status json: %w", err)
	}

	return payload, nil
}

func writeHandshake(w io.Writer, host string, port int) error {
	var packet bytes.Buffer
	packet.Write(varInt(0x00)) // packet ID
	packet.Write(varInt(776))  // protocol version (Java Edition 26.2); unvalidated by the server for a status ping
	packet.Write(packString(host))

	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, uint16(port))
	packet.Write(portBytes)

	packet.Write(varInt(1)) // next state: 1 = status

	return writePacket(w, packet.Bytes())
}

func writePacket(w io.Writer, data []byte) error {
	if _, err := w.Write(varInt(int32(len(data)))); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}

func readPacket(r *bufio.Reader) ([]byte, error) {
	length, err := readVarInt(r)
	if err != nil {
		return nil, fmt.Errorf("read length: %w", err)
	}
	if length < 0 || length > maxResponseLength {
		return nil, fmt.Errorf("invalid packet length: %d", length)
	}

	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return data, nil
}

func packString(s string) []byte {
	b := []byte(s)
	return append(varInt(int32(len(b))), b...)
}

func varInt(n int32) []byte {
	buf := make([]byte, 0, 5)
	u := uint32(n)
	for {
		b := byte(u & 0x7F)
		u >>= 7
		if u != 0 {
			b |= 0x80
		}
		buf = append(buf, b)
		if u == 0 {
			break
		}
	}
	return buf
}

type byteReader interface {
	io.Reader
	io.ByteReader
}

func readVarInt(r byteReader) (int32, error) {
	var result int32
	var shift uint
	for {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		result |= int32(b&0x7F) << shift
		if b&0x80 == 0 {
			break
		}
		shift += 7
		if shift >= 35 {
			return 0, fmt.Errorf("varint too long")
		}
	}
	return result, nil
}
