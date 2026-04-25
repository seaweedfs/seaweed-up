package probe

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/inventory"
)

// scriptedOp wraps scriptedRunner with the rest of operator.CommandOperator
// so probeDisks (which downcasts to the full interface for
// disks.ListBlockDevices) can be exercised with canned lsblk output.
type scriptedOp struct {
	scriptedRunner
}

func (s *scriptedOp) Execute(string) error                    { return fmt.Errorf("scriptedOp: Execute not supported") }
func (s *scriptedOp) Upload(io.Reader, string, string) error  { return fmt.Errorf("scriptedOp: Upload not supported") }
func (s *scriptedOp) UploadFile(string, string, string) error { return fmt.Errorf("scriptedOp: UploadFile not supported") }

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

func TestProbeFstab_parsesEntries(t *testing.T) {
	// Confirm that probeFstab picks up UUID= and /dev/ entries and
	// ignores comments / LABEL= / blank lines.
	r := &scriptedRunner{responses: []scriptedResponse{
		{contains: "/etc/fstab", out: `# /etc/fstab — generated
UUID=abc-123 /data1 ext4 noatime 0 2
UUID="def-456" /data2 ext4 defaults 0 2
/dev/sdd /home ext4 defaults 0 2
LABEL=swap none swap sw 0 0
proc /proc proc defaults 0 0

# trailing comment
`},
	}}
	byUUID, byPath := probeFstab(r)
	if got := byUUID["abc-123"]; got != "/data1" {
		t.Errorf("byUUID[abc-123] = %q, want /data1", got)
	}
	if got := byUUID["def-456"]; got != "/data2" {
		t.Errorf("byUUID[def-456] (quoted form) = %q, want /data2", got)
	}
	if got := byPath["/dev/sdd"]; got != "/home" {
		t.Errorf("byPath[/dev/sdd] = %q, want /home", got)
	}
	if _, ok := byUUID["swap"]; ok {
		t.Error("LABEL= entries should not appear in byUUID")
	}
}

func TestProbeFstab_unreadableReturnsEmpty(t *testing.T) {
	r := &scriptedRunner{responses: []scriptedResponse{
		{contains: "/etc/fstab", err: fmt.Errorf("cat: /etc/fstab: Permission denied")},
	}}
	byUUID, byPath := probeFstab(r)
	if len(byUUID) != 0 || len(byPath) != 0 {
		t.Errorf("expected empty maps on read error, got %d/%d entries", len(byUUID), len(byPath))
	}
}

func TestProbeDisks_picksUpFstabClaim(t *testing.T) {
	// A disk with FSType set but no current MountPoint should pick up
	// its mountpoint from /etc/fstab so the planner can recognize it
	// as cluster-owned even before the mount is realized.
	lsblkOut := strings.Join([]string{
		`KNAME="sdb" PATH="/dev/sdb" SIZE="500000000000" LABEL="" UUID="11111111-aaaa-bbbb-cccc-222222222222" FSTYPE="ext4" TYPE="disk" MOUNTPOINT="" MAJ:MIN="8:16" FSUSED="" ROTA="0" MODEL="Data SSD"`,
	}, "\n") + "\n"
	op := &scriptedOp{scriptedRunner: scriptedRunner{responses: []scriptedResponse{
		{contains: "lsblk", out: lsblkOut},
		{contains: "/etc/fstab", out: "UUID=11111111-aaaa-bbbb-cccc-222222222222 /data1 ext4 noatime 0 2\n"},
	}}}

	disks := probeDisks(op, []string{"/dev/sd*"})
	if len(disks) != 1 {
		t.Fatalf("got %d disks, want 1", len(disks))
	}
	if disks[0].FstabMountPoint != "/data1" {
		t.Errorf("FstabMountPoint = %q, want /data1", disks[0].FstabMountPoint)
	}
	if disks[0].MountPoint != "" {
		t.Errorf("MountPoint = %q, want empty (kernel sees it unmounted)", disks[0].MountPoint)
	}
}

func TestProbeDisks_dropsPartitionedParent(t *testing.T) {
	// Regression: boot disks (e.g. /dev/sda with /dev/sda1 mounted at /)
	// used to pass through as eligible disks in the probe because
	// probeDisks only filtered by Type=="disk". The parent carries no
	// direct FSType / MountPoint — those live on the partitions — so a
	// naive filter would happily offer the boot disk up for mkfs.
	//
	// lsblk output format is KEY="value" space-separated pairs, one
	// record per line.
	lsblkOut := strings.Join([]string{
		`KNAME="sda" PATH="/dev/sda" SIZE="500000000000" LABEL="" UUID="" FSTYPE="" TYPE="disk" MOUNTPOINT="" MAJ:MIN="8:0" FSUSED="" ROTA="1" MODEL="Host Boot Disk"`,
		`KNAME="sda1" PATH="/dev/sda1" SIZE="499000000000" LABEL="" UUID="11111111-1111-1111-1111-111111111111" FSTYPE="ext4" TYPE="part" MOUNTPOINT="/" MAJ:MIN="8:1" FSUSED="" ROTA="1" MODEL=""`,
		`KNAME="sdb" PATH="/dev/sdb" SIZE="1000000000000" LABEL="" UUID="" FSTYPE="" TYPE="disk" MOUNTPOINT="" MAJ:MIN="8:16" FSUSED="" ROTA="1" MODEL="Data HDD"`,
	}, "\n") + "\n"
	op := &scriptedOp{scriptedRunner: scriptedRunner{responses: []scriptedResponse{
		{contains: "lsblk", out: lsblkOut},
	}}}

	disks := probeDisks(op, []string{"/dev/sd*"})
	if len(disks) != 1 {
		t.Fatalf("expected 1 eligible disk (partitioned parent dropped), got %d: %+v", len(disks), disks)
	}
	if disks[0].Path != "/dev/sdb" {
		t.Errorf("got %q, want /dev/sdb", disks[0].Path)
	}
}

func TestProbeNetworkText_preservesIPv6ZoneID(t *testing.T) {
	// Link-local IPv6 addresses carry a zone id (fe80::1%eth0). Dropping
	// the zone loses routing-significant information on multi-interface
	// hosts. Regression test for the truncation reported on PR #67.
	r := &scriptedRunner{responses: []scriptedResponse{
		{contains: "ip -j addr", out: ""}, // force text-mode fallback
		{contains: "ip addr", out: `2: eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc fq_codel state UP group default qlen 1000
    link/ether aa:bb:cc:dd:ee:ff brd ff:ff:ff:ff:ff:ff
    inet 10.0.0.5/24 brd 10.0.0.255 scope global eth0
    inet6 fe80::aabb:ccff:fedd:eeff%eth0/64 scope link
       valid_lft forever preferred_lft forever
`},
		{contains: "/sys/class/net/'eth0'/speed", out: "1000\n"},
	}}

	got := probeNetwork(r)
	if len(got) != 1 {
		t.Fatalf("expected 1 iface, got %d: %+v", len(got), got)
	}
	addrs := got[0].Addresses
	if len(addrs) != 2 {
		t.Fatalf("addresses: got %+v, want 2 entries", addrs)
	}
	if addrs[0] != "10.0.0.5" {
		t.Errorf("ipv4: got %q, want 10.0.0.5", addrs[0])
	}
	if addrs[1] != "fe80::aabb:ccff:fedd:eeff%eth0" {
		t.Errorf("ipv6 zone id dropped: got %q", addrs[1])
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
