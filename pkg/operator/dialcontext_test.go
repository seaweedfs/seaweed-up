package operator

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// With no jump host configured, DialContext connects directly — the fallback
// that keeps status/upgrade probes working on flat networks.
func TestDialContextDirectWhenNoBastion(t *testing.T) {
	SetBastion(nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	client := &http.Client{Transport: &http.Transport{DialContext: DialContext}}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET via DialContext: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("body = %q, want %q", string(body), "ok")
	}
}

// When a jump host is configured but unreachable, DialContext returns an error
// (it routes through the bastion) rather than silently connecting directly.
func TestDialContextRoutesThroughBastion(t *testing.T) {
	SetBastion(&BastionConfig{Host: "127.0.0.1:1"}) // nothing listening there
	defer SetBastion(nil)

	if _, err := DialContext(context.Background(), "tcp", "10.255.255.1:9333"); err == nil {
		t.Fatal("expected an error dialing through an unreachable bastion, got nil")
	}
}
