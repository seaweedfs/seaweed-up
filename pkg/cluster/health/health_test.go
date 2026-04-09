package health

import (
	"context"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"gopkg.in/yaml.v3"
)

// pemEncodeCert wraps a raw DER cert in a PEM block for writing to disk.
func pemEncodeCert(t *testing.T, der []byte) []byte {
	t.Helper()
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

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

// TestNewProberForSpecTLS verifies that NewProberForSpec builds an HTTPS
// prober that trusts a CA loaded from disk when the cluster spec has TLS
// enabled. It uses httptest.NewTLSServer and writes the server's cert as
// "ca.crt" into a temp directory (since httptest self-signs its own leaf).
func TestNewProberForSpecTLS(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Version":"3.75"}`))
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	// Persist the server's self-signed cert as ca.crt in a temp dir so
	// NewProberForSpec picks it up as the trust root.
	dir := t.TempDir()
	caPath := dir + "/ca.crt"
	certPEM := srv.Certificate()
	pemBytes := pemEncodeCert(t, certPEM.Raw)
	if err := os.WriteFile(caPath, pemBytes, 0o600); err != nil {
		t.Fatalf("write ca.crt: %v", err)
	}

	ip, port := parseHostPort(t, srv.URL)
	s := &spec.Specification{
		GlobalOptions: spec.GlobalOptions{TLSEnabled: true},
		VolumeServers: []*spec.VolumeServerSpec{{Ip: ip, Port: port}},
	}
	p := NewProberForSpec(2*time.Second, s, dir)
	if p.Scheme != "https" {
		t.Fatalf("expected scheme https, got %q", p.Scheme)
	}
	res := p.ProbeVolume(context.Background(), ip, port)
	if !res.Healthy {
		t.Fatalf("expected healthy over TLS with CA trust, got err=%s", res.Err)
	}
	if res.Version != "3.75" {
		t.Errorf("version = %q, want 3.75", res.Version)
	}
}

// TestDeployPersistsTLSEnabledAndProbeHTTPS simulates a `cluster deploy
// --tls` flow: stamp TLSEnabled on the spec, persist it via the state
// store, reload, and confirm both the persisted flag and the probe URL
// scheme round-trip correctly.
func TestDeployPersistsTLSEnabledAndProbeHTTPS(t *testing.T) {
	// Emulate the cmd/cluster_impl.go deploy path: when --tls is
	// specified the spec gets TLSEnabled=true before it is written out.
	sp := &spec.Specification{
		Name: "tlscluster",
		MasterServers: []*spec.MasterServerSpec{
			{Ip: "10.0.0.1", Port: 9333},
		},
	}
	sp.GlobalOptions.TLSEnabled = true

	// Round-trip through YAML the same way state.Store does to be sure
	// the `enable_tls` field serializes.
	yml, err := yaml.Marshal(sp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(yml), "enable_tls: true") {
		t.Fatalf("expected enable_tls in YAML, got:\n%s", yml)
	}
	var loaded spec.Specification
	if err := yaml.Unmarshal(yml, &loaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !loaded.GlobalOptions.TLSEnabled {
		t.Fatalf("TLSEnabled did not round-trip")
	}

	// Build a Prober from the reloaded spec and check that it speaks
	// HTTPS (no CA dir -> system roots, which is fine here because we
	// only inspect scheme/URL construction, not an actual handshake).
	p := NewProberForSpec(time.Second, &loaded, "")
	if p.Scheme != "https" {
		t.Fatalf("Scheme = %q, want https", p.Scheme)
	}
	if got := p.scheme(); got != "https" {
		t.Fatalf("scheme() = %q, want https", got)
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
