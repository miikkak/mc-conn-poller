package poller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPPingSucceedsOn200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := httpPing(context.Background(), srv.URL, time.Second); err != nil {
		t.Errorf("httpPing() = %v, want nil", err)
	}
}

func TestHTTPPingFailsOnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if err := httpPing(context.Background(), srv.URL, time.Second); err == nil {
		t.Error("httpPing() = nil, want error for 500 response")
	}
}

func TestHTTPPingFailsOnUnreachableHost(t *testing.T) {
	if err := httpPing(context.Background(), "http://127.0.0.1:1", 100*time.Millisecond); err == nil {
		t.Error("httpPing() = nil, want error for unreachable host")
	}
}
