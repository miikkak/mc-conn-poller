package probe

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

func serverAddr(t *testing.T, srv *httptest.Server) (string, int) {
	t.Helper()
	h, p, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	port, err := strconv.Atoi(p)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return h, port
}

func TestNetherNetPing(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
	}{
		{"valid status", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"protocol":2225,"version":"26.60","nonce":"abc"}`))
		}, false},
		{"missing protocol", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"version":"26.60"}`))
		}, true},
		{"trailing data", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"protocol":2225}not-json`))
		}, true},
		{"trailing whitespace", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("{\"protocol\":2225}\n"))
		}, false},
		{"redirect to a healthy endpoint", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/ok", http.StatusFound)
		}, true},
		{"html body", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`<html></html>`))
		}, true},
		{"not found", func(w http.ResponseWriter, _ *http.Request) {
			http.NotFound(w, nil)
		}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var (
				mu      sync.Mutex
				gotPath string
			)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/ok" {
					_, _ = w.Write([]byte(`{"protocol":1}`))
					return
				}
				mu.Lock()
				gotPath = r.URL.Path
				mu.Unlock()
				tc.handler(w, r)
			}))
			defer srv.Close()
			host, port := serverAddr(t, srv)

			err := Check(context.Background(), "bedrock", host, port, 2*time.Second, IPv4,
				Options{Transport: TransportNetherNet, SignalingScheme: "http"})
			if (err != nil) != tc.wantErr {
				t.Fatalf("Check() error = %v, wantErr %v", err, tc.wantErr)
			}
			mu.Lock()
			defer mu.Unlock()
			if gotPath != "/v1/join" {
				t.Errorf("requested path %q, want /v1/join", gotPath)
			}
		})
	}
}

func TestAutoTransportFallsBackToRakNetError(t *testing.T) {
	// Nothing answers either transport: auto must fail, not succeed.
	host, port := silentUDPListener(t)
	err := Check(context.Background(), "bedrock", host, port, 200*time.Millisecond, IPv4, Options{SignalingScheme: "http"})
	if err == nil {
		t.Fatal("Check() = nil, want error when neither transport answers")
	}
}

func TestAutoTransportSucceedsViaNetherNet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"protocol":1}`))
	}))
	defer srv.Close()
	host, port := serverAddr(t, srv)
	if err := Check(context.Background(), "bedrock", host, port, 2*time.Second, IPv4, Options{SignalingScheme: "http"}); err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
}

func TestUnknownTransportRejected(t *testing.T) {
	if err := Check(context.Background(), "bedrock", "127.0.0.1", 1, time.Second, IPv4, Options{Transport: "quic"}); err == nil {
		t.Error("Check() = nil, want error for unknown transport")
	}
}
