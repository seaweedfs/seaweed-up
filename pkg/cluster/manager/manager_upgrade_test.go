package manager

import (
	"testing"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

// TestBuildUpgradeTargets verifies the rolling upgrade covers every restartable
// component (not just volume/filer/master), in dependency order, with the right
// per-component health probe.
func TestBuildUpgradeTargets(t *testing.T) {
	s := &spec.Specification{
		MasterServers: []*spec.MasterServerSpec{{Ip: "10.0.0.1", Port: 9333, PortSsh: 22}},
		VolumeServers: []*spec.VolumeServerSpec{{Ip: "10.0.0.1", Port: 8080, PortSsh: 22}},
		FilerServers:  []*spec.FilerServerSpec{{Ip: "10.0.0.1", Port: 8888, PortSsh: 22}},
		S3Servers:     []*spec.S3ServerSpec{{Ip: "10.0.0.1", Port: 8333, PortSsh: 22}},
		AdminServers:  []*spec.AdminServerSpec{{Ip: "10.0.0.1", Port: 23646, PortSsh: 22}},
		WorkerServers: []*spec.WorkerServerSpec{{Ip: "10.0.0.1", PortSsh: 22, Admin: "10.0.0.1:23646"}},
	}

	targets := buildUpgradeTargets(s, "http")

	// Order: volume -> filer -> master -> s3 -> admin -> worker.
	wantOrder := []string{"volume", "filer", "master", "s3", "admin", "worker"}
	if len(targets) != len(wantOrder) {
		t.Fatalf("got %d targets, want %d (%v)", len(targets), len(wantOrder), wantOrder)
	}
	for i, want := range wantOrder {
		if targets[i].component != want {
			t.Errorf("target[%d] component = %q, want %q", i, targets[i].component, want)
		}
	}

	byComponent := map[string]upgradeTarget{}
	for _, tg := range targets {
		byComponent[tg.component] = tg
	}

	// Health probes hit the node's advertised Ip:port (never localhost).
	cases := []struct {
		component string
		url       string
		codes     string // "" => default 2xx
	}{
		{"volume", "http://10.0.0.1:8080/status", ""},
		{"filer", "http://10.0.0.1:8888/", ""},
		{"master", "http://10.0.0.1:9333/cluster/status", ""},
		{"s3", "http://10.0.0.1:8333/status", ""},
		{"admin", "http://10.0.0.1:23646/", "2??|3??"},
	}
	for _, c := range cases {
		tg := byComponent[c.component]
		if tg.healthURL != c.url {
			t.Errorf("%s healthURL = %q, want %q", c.component, tg.healthURL, c.url)
		}
		if tg.healthCodes != c.codes {
			t.Errorf("%s healthCodes = %q, want %q", c.component, tg.healthCodes, c.codes)
		}
	}

	// Workers expose no HTTP listener: empty healthURL signals the
	// systemctl is-active fallback gate.
	if w := byComponent["worker"]; w.healthURL != "" {
		t.Errorf("worker healthURL = %q, want empty (systemd is-active gate)", w.healthURL)
	}
}

// TestBuildUpgradeTargets_TLSScheme confirms the probe scheme follows the
// cluster TLS flag.
func TestBuildUpgradeTargets_TLSScheme(t *testing.T) {
	s := &spec.Specification{
		MasterServers: []*spec.MasterServerSpec{{Ip: "10.0.0.1", Port: 9333, PortSsh: 22}},
	}
	targets := buildUpgradeTargets(s, "https")
	if len(targets) != 1 {
		t.Fatalf("got %d targets, want 1", len(targets))
	}
	if got, want := targets[0].healthURL, "https://10.0.0.1:9333/cluster/status"; got != want {
		t.Errorf("healthURL = %q, want %q", got, want)
	}
}
