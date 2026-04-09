package manager

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/scripts"
)

// fakeOperator records uploads and executed commands for assertions.
type fakeOperator struct {
	executed []string
	uploads  map[string]string
	execErr  error
}

func newFakeOperator() *fakeOperator {
	return &fakeOperator{uploads: map[string]string{}}
}

func (f *fakeOperator) Execute(cmd string) error {
	f.executed = append(f.executed, cmd)
	return f.execErr
}

func (f *fakeOperator) Output(cmd string) ([]byte, error) {
	f.executed = append(f.executed, cmd)
	return nil, f.execErr
}

func (f *fakeOperator) Upload(src io.Reader, remotePath string, mode string) error {
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(src); err != nil {
		return err
	}
	f.uploads[remotePath] = buf.String()
	return nil
}

func (f *fakeOperator) UploadFile(path string, remotePath string, mode string) error {
	f.uploads[remotePath] = "file:" + path
	return nil
}

func TestHostPrepScriptEmbedded(t *testing.T) {
	if scripts.HostPrepScript == "" {
		t.Fatal("HostPrepScript is empty; go:embed did not load host_prep.sh")
	}
	mustContain := []string{
		"/etc/security/limits.d/seaweed.conf",
		"/etc/sysctl.d/99-seaweed.conf",
		"vm.max_map_count=262144",
		"net.core.somaxconn=4096",
		"fs.file-max=2097152",
		"9333",
		"19333",
		"8080",
		"18080",
		"8888",
		"18888",
		"8333",
		"23646",
		"chrony",
		"systemd-timesyncd",
		"ufw",
		"firewall-cmd",
		"iptables",
		"sysctl --system",
		"nofile 1048576",
	}
	for _, s := range mustContain {
		if !strings.Contains(scripts.HostPrepScript, s) {
			t.Errorf("HostPrepScript missing expected snippet %q", s)
		}
	}
}

func TestPrepareHostUploadsAndRuns(t *testing.T) {
	m := NewManager()
	op := newFakeOperator()

	if err := m.PrepareHost(op); err != nil {
		t.Fatalf("PrepareHost returned error: %v", err)
	}

	// Should have uploaded exactly one host_prep.sh file.
	var found bool
	for path, content := range op.uploads {
		if strings.HasSuffix(path, "/host_prep.sh") {
			found = true
			if !strings.Contains(content, "vm.max_map_count=262144") {
				t.Errorf("uploaded host_prep.sh missing sysctl config")
			}
		}
	}
	if !found {
		t.Error("expected host_prep.sh to be uploaded")
	}

	// Should have executed a mkdir, the script, and a cleanup rm -rf.
	var sawMkdir, sawRun, sawCleanup bool
	for _, cmd := range op.executed {
		if strings.HasPrefix(cmd, "mkdir -p /tmp/seaweed-up.") {
			sawMkdir = true
		}
		if strings.Contains(cmd, "host_prep.sh") && strings.Contains(cmd, "SUDO_PASS=") {
			sawRun = true
		}
		if strings.HasPrefix(cmd, "rm -rf /tmp/seaweed-up.") {
			sawCleanup = true
		}
	}
	if !sawMkdir {
		t.Error("expected mkdir command")
	}
	if !sawRun {
		t.Error("expected host_prep.sh to be executed with SUDO_PASS")
	}
	if !sawCleanup {
		t.Error("expected cleanup rm -rf command")
	}
}

func TestPrepareAllHostsDeduplicatesByIP(t *testing.T) {
	// Spec has the same host colocated across master/volume/filer plus a
	// distinct envoy host. PrepareAllHosts should invoke the prepare hook
	// exactly once per unique ip:port.
	s := &spec.Specification{
		MasterServers: []*spec.MasterServerSpec{{Ip: "10.0.0.1", PortSsh: 22}},
		VolumeServers: []*spec.VolumeServerSpec{{Ip: "10.0.0.1", PortSsh: 22}},
		FilerServers:  []*spec.FilerServerSpec{{Ip: "10.0.0.1", PortSsh: 22}},
		EnvoyServers:  []*spec.EnvoyServerSpec{{Ip: "10.0.0.2", PortSsh: 2222}},
	}

	m := NewManager()
	type call struct {
		ip   string
		port int
	}
	var calls []call
	m.prepareHostAddressFn = func(ip string, sshPort int) error {
		calls = append(calls, call{ip: ip, port: sshPort})
		return nil
	}

	if err := m.PrepareAllHosts(s); err != nil {
		t.Fatalf("PrepareAllHosts returned error: %v", err)
	}

	if len(calls) != 2 {
		t.Fatalf("expected 2 unique host prepare calls, got %d: %+v", len(calls), calls)
	}
	if calls[0] != (call{ip: "10.0.0.1", port: 22}) {
		t.Errorf("first call = %+v, want {10.0.0.1 22}", calls[0])
	}
	if calls[1] != (call{ip: "10.0.0.2", port: 2222}) {
		t.Errorf("second call = %+v, want {10.0.0.2 2222}", calls[1])
	}
}

func TestPrepareAllHostsPropagatesError(t *testing.T) {
	s := &spec.Specification{
		MasterServers: []*spec.MasterServerSpec{{Ip: "10.0.0.1", PortSsh: 22}},
	}
	m := NewManager()
	m.prepareHostAddressFn = func(ip string, sshPort int) error {
		return io.EOF
	}
	if err := m.PrepareAllHosts(s); err == nil {
		t.Fatal("expected error from PrepareAllHosts, got nil")
	}
}
