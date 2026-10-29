package poller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestHTTPPushKumaSendsStatusUpWithLatency(t *testing.T) {
	var gotMethod, gotUA, gotPath string
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotUA, gotPath, gotQuery = r.Method, r.Header.Get("User-Agent"), r.URL.Path, r.URL.Query()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := httpPushKuma(context.Background(), srv.URL+"/api/push/tok", time.Second, testReport()); err != nil {
		t.Fatalf("httpPushKuma() = %v, want nil", err)
	}

	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/api/push/tok" {
		t.Errorf("path = %q, want /api/push/tok", gotPath)
	}
	if want := "mc-conn-poller/1.2.3 (poller-a)"; gotUA != want {
		t.Errorf("User-Agent = %q, want %q", gotUA, want)
	}
	for k, want := range map[string]string{"status": "up", "msg": "OK", "ping": "42"} {
		if got := gotQuery.Get(k); got != want {
			t.Errorf("query %s = %q, want %q", k, got, want)
		}
	}
}

func TestHTTPPushKumaOverridesQueryFromKumaUI(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// The URL as copied from Kuma, plus an unrelated parameter that must survive.
	pushURL := srv.URL + "/api/push/tok?status=up&msg=OK&ping=&keep=1"
	if err := httpPushKuma(context.Background(), pushURL, time.Second, testReport()); err != nil {
		t.Fatalf("httpPushKuma() = %v, want nil", err)
	}

	for _, k := range []string{"status", "msg", "ping"} {
		if n := len(gotQuery[k]); n != 1 {
			t.Errorf("query %s has %d values, want 1 (%v)", k, n, gotQuery[k])
		}
	}
	if got := gotQuery.Get("ping"); got != "42" {
		t.Errorf("ping = %q, want 42", got)
	}
	if got := gotQuery.Get("keep"); got != "1" {
		t.Errorf("keep = %q, want 1", got)
	}
}

func TestHTTPPushKumaFailsOnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound) // Kuma answers 404 for an unknown token
	}))
	defer srv.Close()

	if err := httpPushKuma(context.Background(), srv.URL+"/api/push/tok", time.Second, testReport()); err == nil {
		t.Error("httpPushKuma() = nil, want error for 404 response")
	}
}

func TestHTTPPushKumaFailsOnTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)

	if err := httpPushKuma(context.Background(), srv.URL+"/api/push/tok", 50*time.Millisecond, testReport()); err == nil {
		t.Error("httpPushKuma() = nil, want error for timeout")
	}
}

func TestHTTPPushKumaErrorDoesNotLeakToken(t *testing.T) {
	const secret = "SECRET-PUSH-TOKEN"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, r.URL.Path, http.StatusFound) // redirect loop
	}))
	defer srv.Close()

	tests := []struct {
		name string
		url  string
	}{
		{"unreachable host", "http://127.0.0.1:1/api/push/" + secret},
		{"redirect loop", srv.URL + "/api/push/" + secret},
		{"unparsable url", "http://127.0.0.1:1/api/push/" + secret + "%zz"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := httpPushKuma(context.Background(), tc.url, time.Second, testReport())
			if err == nil {
				t.Fatal("httpPushKuma() = nil, want error")
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("error %q leaks the push token", err)
			}
		})
	}
}
