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
