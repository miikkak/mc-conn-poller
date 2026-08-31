package poller

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"context"

	"github.com/miikkak/mc-conn-poller/internal/probe"
)

func testReport() PingReport {
	return PingReport{
		PollerInfo: Info{Version: "1.2.3", Host: "poller-a"},
		Target:     "survival",
		Protocol:   "java",
		Host:       "mc.example.invalid",
		Port:       25565,
		IPFamily:   probe.IPv4,
		Latency:    42 * time.Millisecond,
	}
}

func TestHTTPPingSucceedsOn200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := httpPing(context.Background(), srv.URL, time.Second, testReport()); err != nil {
		t.Errorf("httpPing() = %v, want nil", err)
	}
}

func TestHTTPPingFailsOnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if err := httpPing(context.Background(), srv.URL, time.Second, testReport()); err == nil {
		t.Error("httpPing() = nil, want error for 500 response")
	}
}

func TestHTTPPingFailsOnUnreachableHost(t *testing.T) {
	if err := httpPing(context.Background(), "http://127.0.0.1:1", 100*time.Millisecond, testReport()); err == nil {
		t.Error("httpPing() = nil, want error for unreachable host")
	}
}

func TestHTTPPingSendsUserAgentAndBody(t *testing.T) {
	var gotUA, gotMethod, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotMethod = r.Method
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := httpPing(context.Background(), srv.URL, time.Second, testReport()); err != nil {
		t.Fatalf("httpPing() = %v, want nil", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if want := "mc-conn-poller/1.2.3 (poller-a)"; gotUA != want {
		t.Errorf("User-Agent = %q, want %q", gotUA, want)
	}
	for _, want := range []string{"poller_host=poller-a", "target=survival", "ip_family=4", "protocol=java", "port=25565"} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("body = %q, want it to contain %q", gotBody, want)
		}
	}
}
