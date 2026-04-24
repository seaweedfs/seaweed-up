package probe

import (
	"fmt"
	"strings"
	"testing"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/inventory"
)

// scriptedRunner is a Runner that returns canned outputs keyed by a
// substring match against the command. Tests register a table; the
// matching order is stable (first match wins), so a specific pattern
// should appear before a more general one.
type scriptedRunner struct {
	responses []scriptedResponse
}

type scriptedResponse struct {
	contains string
	out      string
	err      error
}

func (s *scriptedRunner) Output(cmd string) ([]byte, error) {
	for _, r := range s.responses {
		if strings.Contains(cmd, r.contains) {
			if r.err != nil {
				return nil, r.err
			}
			return []byte(r.out), nil
		}
	}
	return nil, fmt.Errorf("no scripted response for %q", cmd)
}

func TestProbeInto_populatesAllFields(t *testing.T) {
	r := &scriptedRunner{responses: []scriptedResponse{
		{contains: "hostname", out: "volume-1\n"},
		{contains: "/etc/os-release", out: "ubuntu\n22.04\n"},
		{contains: "uname -m", out: "x86_64\n"},
		{contains: "^processor", out: "8\n"},
		{contains: "MemTotal", out: "16777216\n"}, // kB → 16 GiB
		{contains: "ip -j addr", out: `[{"ifname":"eth0","addr_info":[{"family":"inet","local":"10.0.0.21"}]}]`},
		{contains: "/sys/class/net/'eth0'/speed", out: "10000\n"},
	}}

	inv := &inventory.Inventory{}
	h := &inventory.Host{IP: "10.0.0.21"}
	facts := HostFacts{IP: h.IP}
	probeInto(r, inv, h, &facts)

	if facts.Hostname != "volume-1" {
		t.Errorf("hostname: got %q", facts.Hostname)
	}
	if facts.OS != "ubuntu" || facts.OSVersion != "22.04" {
		t.Errorf("os: got %q/%q", facts.OS, facts.OSVersion)
	}
	if facts.Arch != "amd64" {
		t.Errorf("arch: got %q, want amd64 (x86_64 should be translated)", facts.Arch)
	}
	if facts.CPUCores != 8 {
		t.Errorf("cpu: got %d", facts.CPUCores)
	}
	if facts.MemoryBytes != 16777216*1024 {
		t.Errorf("memory: got %d bytes", facts.MemoryBytes)
	}
	if len(facts.NetIfaces) != 1 || facts.NetIfaces[0].Name != "eth0" {
		t.Fatalf("net: got %+v", facts.NetIfaces)
	}
	if facts.NetIfaces[0].SpeedMbps != 10000 {
		t.Errorf("net speed: got %d", facts.NetIfaces[0].SpeedMbps)
	}
	if len(facts.NetIfaces[0].Addresses) != 1 || facts.NetIfaces[0].Addresses[0] != "10.0.0.21" {
		t.Errorf("net addresses: got %+v", facts.NetIfaces[0].Addresses)
	}
}

func TestProbeNetwork_fallsBackToTextWhenJSONMissing(t *testing.T) {
	// Simulate an old iproute2: `ip -j addr` returns empty (the flag is
	// silently dropped on some builds), but text `ip addr` works.
	r := &scriptedRunner{responses: []scriptedResponse{
		{contains: "ip -j addr", out: ""},
		{contains: "ip addr", out: `1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN group default qlen 1000
    link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00
    inet 127.0.0.1/8 scope host lo
2: eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc fq_codel state UP group default qlen 1000
    link/ether aa:bb:cc:dd:ee:ff brd ff:ff:ff:ff:ff:ff
    inet 10.0.0.5/24 brd 10.0.0.255 scope global eth0
       valid_lft forever preferred_lft forever
`},
		{contains: "/sys/class/net/'eth0'/speed", out: "1000\n"},
	}}

	got := probeNetwork(r)
	if len(got) != 1 {
		t.Fatalf("expected 1 iface (lo filtered), got %d: %+v", len(got), got)
	}
	if got[0].Name != "eth0" {
		t.Errorf("name: got %q", got[0].Name)
	}
	if len(got[0].Addresses) == 0 || got[0].Addresses[0] != "10.0.0.5" {
		t.Errorf("addresses: got %+v", got[0].Addresses)
	}
	if got[0].SpeedMbps != 1000 {
		t.Errorf("speed: got %d", got[0].SpeedMbps)
	}
}

func TestProbeOS_missingOSRelease(t *testing.T) {
	r := &scriptedRunner{responses: []scriptedResponse{
		{contains: "/etc/os-release", err: fmt.Errorf("exit 1")},
	}}
	id, ver := probeOS(r)
	if id != "" || ver != "" {
		t.Errorf("missing /etc/os-release: got %q/%q, want empty/empty", id, ver)
	}
}

func TestProbeArch_translatesKernelNames(t *testing.T) {
	cases := map[string]string{
		"x86_64\n":  "amd64",
		"aarch64\n": "arm64",
		"riscv64\n": "riscv64", // pass-through for anything we don't translate
	}
	for in, want := range cases {
		r := &scriptedRunner{responses: []scriptedResponse{
			{contains: "uname -m", out: in},
		}}
		if got := probeArch(r); got != want {
			t.Errorf("probeArch(%q): got %q, want %q", in, got, want)
		}
	}
}

func TestReadSpeed_negativeReturnsZero(t *testing.T) {
	// Virtual NICs and some hypervisor drivers report -1.
	r := &scriptedRunner{responses: []scriptedResponse{
		{contains: "/sys/class/net/'virt0'/speed", out: "-1\n"},
	}}
	if got := readSpeed(r, "virt0"); got != 0 {
		t.Errorf("readSpeed: got %d, want 0 for -1 kernel report", got)
	}
}

func TestNewHostFacts_carriesSSHTarget(t *testing.T) {
	// Inventories with the same IP but different SSH ports must produce
	// distinguishable records, otherwise downstream consumers cannot map
	// facts back to inventory entries.
	h := &inventory.Host{IP: "10.0.0.1"}
	a := newHostFacts(h, inventory.SSHConfig{Port: 22})
	b := newHostFacts(h, inventory.SSHConfig{Port: 2222})

	if a.IP != "10.0.0.1" || b.IP != "10.0.0.1" {
		t.Errorf("IP: got %q/%q", a.IP, b.IP)
	}
	if a.SSHPort != 22 || b.SSHPort != 2222 {
		t.Errorf("SSHPort: got %d/%d, want 22/2222", a.SSHPort, b.SSHPort)
	}
	if a.ProbedAt.IsZero() || b.ProbedAt.IsZero() {
		t.Error("ProbedAt was not stamped")
	}
}
