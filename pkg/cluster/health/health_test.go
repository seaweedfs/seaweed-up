package health

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

// parseHostPort extracts ip + port from an httptest.Server URL.
func parseHostPort(t *testing.T, raw string) (string, int) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split host/port: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("port: %v", err)
	}
	return host, port
}

func newMasterServer(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/cluster/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"IsLeader":true,"Leader":"127.0.0.1:9333","Version":"3.75"}`))
	})
	mux.HandleFunc("/dir/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Version":"3.75","Topology":{"Free":10}}`))
	})
	return httptest.NewServer(mux)
}

func newVolumeServer(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Version":"3.75","Volumes":[]}`))
	})
	return httptest.NewServer(mux)
}

func newFilerServer(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Real SeaweedFS filer has no /status endpoint; the root returns a
		// directory listing with a "Server: SeaweedFS <version>" header.
		w.Header().Set("Server", "SeaweedFS 3.75")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Path":"/","Entries":[]}`))
	})
	return httptest.NewServer(mux)
}

func TestProbeMaster(t *testing.T) {
	srv := newMasterServer(t)
	defer srv.Close()
	ip, port := parseHostPort(t, srv.URL)

	p := NewProber(2 * time.Second)
	res := p.ProbeMaster(context.Background(), ip, port)
	if !res.Healthy {
		t.Fatalf("expected healthy, got err=%s", res.Err)
	}
	if res.Version != "3.75" {
		t.Errorf("expected version 3.75, got %q", res.Version)
	}
	if res.Extra["dir_status"] == nil {
		t.Errorf("expected dir_status extra data")
	}
}

func TestProbeMasterUnhealthyOnDirStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/cluster/status", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Version":"3.75"}`))
	})
	mux.HandleFunc("/dir/status", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ip, port := parseHostPort(t, srv.URL)

	p := NewProber(2 * time.Second)
	res := p.ProbeMaster(context.Background(), ip, port)
	if res.Healthy {
		t.Fatalf("expected unhealthy")
	}
	if !strings.Contains(res.Err, "dir/status") {
		t.Errorf("expected dir/status error, got %q", res.Err)
	}
}

func TestProbeVolume(t *testing.T) {
	srv := newVolumeServer(t)
	defer srv.Close()
	ip, port := parseHostPort(t, srv.URL)
	p := NewProber(2 * time.Second)
	res := p.ProbeVolume(context.Background(), ip, port)
	if !res.Healthy {
		t.Fatalf("unhealthy: %s", res.Err)
	}
	if res.Version != "3.75" {
		t.Errorf("version = %q", res.Version)
	}
}

func TestProbeFiler(t *testing.T) {
	srv := newFilerServer(t)
	defer srv.Close()
	ip, port := parseHostPort(t, srv.URL)
	p := NewProber(2 * time.Second)
	res := p.ProbeFiler(context.Background(), ip, port)
	if !res.Healthy {
		t.Fatalf("unhealthy: %s", res.Err)
	}
	if res.Version != "3.75" {
		t.Errorf("expected version 3.75 from Server header, got %q", res.Version)
	}
}

func TestProbeFilerNoServerHeader(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`ok`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ip, port := parseHostPort(t, srv.URL)
	p := NewProber(2 * time.Second)
	res := p.ProbeFiler(context.Background(), ip, port)
	if !res.Healthy {
		t.Fatalf("unhealthy: %s", res.Err)
	}
	if res.Version != "" {
		t.Errorf("expected empty version, got %q", res.Version)
	}
}

func TestProbeFilerNon2xx(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ip, port := parseHostPort(t, srv.URL)
	p := NewProber(2 * time.Second)
	res := p.ProbeFiler(context.Background(), ip, port)
	if res.Healthy {
		t.Fatalf("expected unhealthy")
	}
	if !strings.Contains(res.Err, "500") {
		t.Errorf("expected status 500 in err, got %q", res.Err)
	}
}

func TestParseServerHeaderVersion(t *testing.T) {
	cases := map[string]string{
		"":                     "",
		"nginx":                "",
		"SeaweedFS 3.75":       "3.75",
		"SeaweedFS/3.80":       "3.80",
		"SeaweedFS 3.75-beta1": "3.75-beta1",
	}
	for in, want := range cases {
		if got := parseServerHeaderVersion(in); got != want {
			t.Errorf("parseServerHeaderVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProbeUnreachable(t *testing.T) {
	p := NewProber(500 * time.Millisecond)
	// Use 127.0.0.1:1 which should refuse.
	res := p.ProbeVolume(context.Background(), "127.0.0.1", 1)
	if res.Healthy {
		t.Fatal("expected unhealthy for unreachable endpoint")
	}
	if res.Err == "" {
		t.Fatal("expected error message")
	}
}

func TestProbeAggregation(t *testing.T) {
	master := newMasterServer(t)
	defer master.Close()
	volume := newVolumeServer(t)
	defer volume.Close()
	filer := newFilerServer(t)
	defer filer.Close()

	mip, mport := parseHostPort(t, master.URL)
	vip, vport := parseHostPort(t, volume.URL)
	fip, fport := parseHostPort(t, filer.URL)

	s := &spec.Specification{
		MasterServers: []*spec.MasterServerSpec{{Ip: mip, Port: mport}},
		VolumeServers: []*spec.VolumeServerSpec{{Ip: vip, Port: vport}},
		FilerServers:  []*spec.FilerServerSpec{{Ip: fip, Port: fport}},
	}

	p := NewProber(2 * time.Second)
	ch := p.Probe(context.Background(), s)
	if !ch.AllHealthy() {
		t.Fatalf("expected all healthy: %+v", ch)
	}
	if len(ch.Masters) != 1 || len(ch.Volumes) != 1 || len(ch.Filers) != 1 {
		t.Fatalf("wrong counts: %+v", ch)
	}
}

func TestProbeAggregationUnhealthy(t *testing.T) {
	master := newMasterServer(t)
	defer master.Close()
	mip, mport := parseHostPort(t, master.URL)

	s := &spec.Specification{
		MasterServers: []*spec.MasterServerSpec{{Ip: mip, Port: mport}},
		VolumeServers: []*spec.VolumeServerSpec{{Ip: "127.0.0.1", Port: 1}},
	}
	p := NewProber(500 * time.Millisecond)
	ch := p.Probe(context.Background(), s)
	if ch.AllHealthy() {
		t.Fatal("expected unhealthy cluster")
	}
}
