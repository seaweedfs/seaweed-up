package manager

import (
	"strings"
	"testing"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

func TestDeployVectorInstance(t *testing.T) {
	m := &Manager{User: "root", Version: "4.52"}
	op := newFakeOperator()
	op.outputFn = func(cmd string) ([]byte, error) { return []byte("Linux aarch64\n"), nil }
	var probedURL string
	stubReleaseAssetHead(t, func(url string) (int, error) { probedURL = url; return 200, nil })

	v := &spec.VectorServerSpec{Ip: "10.0.0.20", Masters: "10.0.0.1:9333", Config: map[string]interface{}{"read-cache-mb": 1024}}
	if err := m.deployVectorInstance(op, v, 0); err != nil {
		t.Fatalf("deployVectorInstance: %v", err)
	}
	if want := "https://github.com/seaweedfs/seaweedfs/releases/download/4.52/weed-vector_linux_arm64.tar.gz"; probedURL != want {
		t.Errorf("asset probe URL: got %q want %q", probedURL, want)
	}
	var script string
	for path, content := range op.uploads {
		if strings.HasSuffix(path, "/install_vector0.sh") {
			script = content
		}
	}
	if script == "" {
		t.Fatalf("no install_vector0.sh uploaded; uploads: %v", keys(op.uploads))
	}
	for _, want := range []string{
		"BINARY=weed-vector",
		"weed-vector_${OS}_${SUFFIX}.tar.gz",
		".weed-vector-version",
		"ExecStart=${BIN_DIR}/${BINARY} --listen 10.0.0.20:8090 --masters 10.0.0.1:9333 --read-cache-mb 1024",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("vector install script missing %q", want)
		}
	}
	if strings.Contains(script, "weed-worker") || strings.Contains(script, "-logdir=") {
		t.Errorf("vector install script must install weed-vector alone, with its own flags")
	}
}

// The enterprise engine keeps its id under the instance's data directory
// unless the spec names a state-dir; the open-source engine gets none.
func TestDeployVectorInstance_StateDir(t *testing.T) {
	stubReleaseAssetHead(t, func(url string) (int, error) { return 200, nil })
	for _, tc := range []struct {
		name       string
		enterprise bool
		config     map[string]interface{}
		want       string
	}{
		{"enterprise", true, nil, "--state-dir /opt/seaweed/vector0"},
		{"enterprise with state-dir", true, map[string]interface{}{"state-dir": "/srv/vector"}, "--state-dir /srv/vector"},
		{"open source", false, nil, ""},
	} {
		m := &Manager{User: "root", Version: "4.54", Enterprise: tc.enterprise, dataDir: "/opt/seaweed"}
		op := newFakeOperator()
		op.outputFn = func(cmd string) ([]byte, error) { return []byte("Linux x86_64\n"), nil }
		v := &spec.VectorServerSpec{Ip: "10.0.0.20", Masters: "10.0.0.1:9333", Config: tc.config}
		if err := m.deployVectorInstance(op, v, 0); err != nil {
			t.Fatalf("%s: deployVectorInstance: %v", tc.name, err)
		}
		var script string
		for path, content := range op.uploads {
			if strings.HasSuffix(path, "/install_vector0.sh") {
				script = content
			}
		}
		got := strings.Count(script, "--state-dir")
		switch {
		case tc.want == "" && got != 0:
			t.Errorf("%s: open-source engine got --state-dir", tc.name)
		case tc.want != "" && (got != 1 || !strings.Contains(script, tc.want)):
			t.Errorf("%s: want one %q, script has %d --state-dir", tc.name, tc.want, got)
		}
	}
}

// The engine is asked for by name, so what would make the Lance worker skip
// fails the vector deploy instead.
func TestDeployVectorInstance_Refusals(t *testing.T) {
	stubReleaseAssetHead(t, func(url string) (int, error) { return 404, nil })
	m := &Manager{User: "root", Version: "4.40"}
	for _, tc := range []struct {
		name, uname, want string
		v                 *spec.VectorServerSpec
	}{
		{"no masters", "Linux x86_64", "no masters", &spec.VectorServerSpec{Ip: "10.0.0.20"}},
		{"platform", "Darwin arm64", "linux only", &spec.VectorServerSpec{Ip: "10.0.0.20", Masters: "m:9333"}},
		{"release", "Linux x86_64", "does not ship weed-vector_linux_amd64.tar.gz", &spec.VectorServerSpec{Ip: "10.0.0.20", Masters: "m:9333"}},
	} {
		op := newFakeOperator()
		uname := tc.uname
		op.outputFn = func(cmd string) ([]byte, error) { return []byte(uname + "\n"), nil }
		err := m.deployVectorInstance(op, tc.v, 0)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error containing %q", tc.name, err, tc.want)
		}
	}
}

func TestVectorLifecycleNames(t *testing.T) {
	s := &spec.Specification{VectorServers: []*spec.VectorServerSpec{{Ip: "10.0.0.20", PortSsh: 22}, {Ip: "10.0.0.21", PortSsh: 22}}}
	if got := servicesForHost(s, "10.0.0.21", ""); len(got) != 1 || got[0] != "seaweed_vector1.service" {
		t.Errorf("services on 10.0.0.21: %v", got)
	}
	if got := uniqueHosts(s, vectorComponent); len(got) != 2 {
		t.Errorf("vector hosts: %v", got)
	}
}

func TestPrepare_DerivesVectorMasters(t *testing.T) {
	m := &Manager{User: "root"}
	sp := &spec.Specification{
		MasterServers: []*spec.MasterServerSpec{{Ip: "10.0.0.1", Port: 9333}},
		VectorServers: []*spec.VectorServerSpec{{Ip: "10.0.0.20"}, {Ip: "10.0.0.21", Masters: "m.example:9333"}},
	}
	m.prepare(sp)
	if got := sp.VectorServers[0].Masters; got != "10.0.0.1:9333" {
		t.Errorf("derived masters: %q", got)
	}
	if got := sp.VectorServers[1].Masters; got != "m.example:9333" {
		t.Errorf("explicit masters must win: %q", got)
	}
	if sp.VectorServers[0].PortSsh != 22 {
		t.Errorf("ssh port default: %d", sp.VectorServers[0].PortSsh)
	}
}

func TestPrepareVectorServersOnTLS(t *testing.T) {
	s := &spec.Specification{GlobalOptions: spec.GlobalOptions{TLSEnabled: true}, VectorServers: []*spec.VectorServerSpec{{Ip: "10.0.0.20"}, {Ip: "10.0.0.21", Config: map[string]interface{}{"security-toml": "/custom/security.toml"}}}}
	m := &Manager{User: "root"}
	m.prepare(s)
	if got := s.VectorServers[0].Config["security-toml"]; got != "/etc/seaweed/security.toml" {
		t.Fatalf("TLS config = %v", got)
	}
	if got := s.VectorServers[1].Config["security-toml"]; got != "/custom/security.toml" {
		t.Fatalf("custom TLS config = %v", got)
	}
}
