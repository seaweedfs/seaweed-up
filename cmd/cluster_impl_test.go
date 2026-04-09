package cmd

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

func TestExtractSeaweedVersion(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"canonical dir status", "30GB 3.85 b2f34c5", "3.85"},
		{"canonical dir status patch", "30GB 3.85.1 b2f34c5", "3.85.1"},
		{"canonical with space before unit", "30 GB 3.85 commit", "3.85"},
		{"named seaweedfs", "seaweedfs 3.85", "3.85"},
		{"named weed", "weed v3.85.1", "3.85.1"},
		{"server header slash", "SeaweedFS/3.85", "3.85"},
		{"ip only returns empty", "172.28.0.10", ""},
		{"ip embedded returns empty", "master at 172.28.0.10:9333 ok", ""},
		{"commit hash only", "b2f34c5abcdef", ""},
		{"empty string", "", ""},
		{"bare digits", "3.85", ""}, // no SeaweedFS context, must not match
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractSeaweedVersion(tc.in)
			if got != tc.want {
				t.Errorf("extractSeaweedVersion(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// newTestMasterSpec builds a Specification pointing at the given test servers.
func newTestMasterSpec(t *testing.T, servers ...*httptest.Server) *spec.Specification {
	t.Helper()
	s := &spec.Specification{}
	for _, srv := range servers {
		u, err := url.Parse(srv.URL)
		if err != nil {
			t.Fatalf("parse test server url: %v", err)
		}
		host, portStr, err := net.SplitHostPort(u.Host)
		if err != nil {
			t.Fatalf("split host/port: %v", err)
		}
		port, err := strconv.Atoi(portStr)
		if err != nil {
			t.Fatalf("parse port: %v", err)
		}
		s.MasterServers = append(s.MasterServers, &spec.MasterServerSpec{
			Ip:   host,
			Port: port,
		})
	}
	return s
}

func TestProbeCurrentClusterVersion_CanonicalDirStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dir/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"Topology":{"Max":100},"Version":"30GB 3.85 b2f34c5"}`)
	}))
	defer srv.Close()

	got, err := probeCurrentClusterVersion(newTestMasterSpec(t, srv), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "3.85" {
		t.Errorf("got %q, want %q", got, "3.85")
	}
}

func TestProbeCurrentClusterVersion_IPLikeStringNotExtracted(t *testing.T) {
	// Simulate the CI failure mode: a master reports only IP-bearing fields.
	// The previous regex extracted "172.28.0" which broke rollback.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"Leader":"172.28.0.10:9333","Peers":["172.28.0.10:9333","172.28.0.11:9333"]}`)
	}))
	defer srv.Close()

	got, err := probeCurrentClusterVersion(newTestMasterSpec(t, srv), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want \"\" (IP must not be extracted as version)", got)
	}
	if strings.Contains(got, "172") {
		t.Errorf("got %q contains IP bytes, regression against CI failure mode", got)
	}
}

func TestProbeCurrentClusterVersion_NoVersionPresent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()

	got, err := probeCurrentClusterVersion(newTestMasterSpec(t, srv), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestProbeCurrentClusterVersion_ContinuesProbingOtherMasters(t *testing.T) {
	// First master responds 200 but without any version info.
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"Leader":"172.28.0.10:9333"}`)
	}))
	defer srv1.Close()
	// Second master returns the canonical version payload.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dir/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"Version":"30GB 3.85 b2f34c5"}`)
	}))
	defer srv2.Close()

	got, err := probeCurrentClusterVersion(newTestMasterSpec(t, srv1, srv2), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "3.85" {
		t.Errorf("got %q, want %q", got, "3.85")
	}
}

func TestProbeCurrentClusterVersion_AllHealthyButNoVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"Leader":"master:9333"}`)
	}))
	defer srv.Close()

	got, err := probeCurrentClusterVersion(newTestMasterSpec(t, srv), false)
	if err != nil {
		t.Fatalf("unexpected error when healthy masters lack version: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want \"\"", got)
	}
}

func TestProbeCurrentClusterVersion_ServerHeaderFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "SeaweedFS/3.85")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()

	got, err := probeCurrentClusterVersion(newTestMasterSpec(t, srv), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "3.85" {
		t.Errorf("got %q, want %q", got, "3.85")
	}
}
