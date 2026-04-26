package plan

import (
	"testing"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/probe"
)

func host(ip string, paths ...string) probe.HostFacts {
	hf := probe.HostFacts{IP: ip, SSHPort: 22}
	for _, p := range paths {
		hf.Disks = append(hf.Disks, probe.DiskFact{Path: p})
	}
	return hf
}

func TestDetectDrift_noPrev(t *testing.T) {
	// First plan run: nothing to compare against. DetectDrift returns
	// no reports rather than treating every disk as "added drift".
	if got := DetectDrift(nil, []probe.HostFacts{host("10.0.0.21", "/dev/sdb")}); got != nil {
		t.Errorf("nil prev should return nil, got %+v", got)
	}
}

func TestDetectDrift_noFresh(t *testing.T) {
	// Inverse: probe somehow returned no facts. Don't synthesize
	// "everything removed" warnings — the missing probe is the bigger
	// signal and the cmd layer surfaces that separately.
	if got := DetectDrift([]probe.HostFacts{host("10.0.0.21", "/dev/sdb")}, nil); got != nil {
		t.Errorf("nil fresh should return nil, got %+v", got)
	}
}

func TestDetectDrift_noChange(t *testing.T) {
	prev := []probe.HostFacts{host("10.0.0.21", "/dev/sdb", "/dev/sdc")}
	fresh := []probe.HostFacts{host("10.0.0.21", "/dev/sdb", "/dev/sdc")}
	if got := DetectDrift(prev, fresh); got != nil {
		t.Errorf("identical disk sets should produce no drift, got %+v", got)
	}
}

func TestDetectDrift_diskAdded(t *testing.T) {
	prev := []probe.HostFacts{host("10.0.0.21", "/dev/sdb")}
	fresh := []probe.HostFacts{host("10.0.0.21", "/dev/sdb", "/dev/sdc")}
	got := DetectDrift(prev, fresh)
	if len(got) != 1 {
		t.Fatalf("expected one drift report, got %+v", got)
	}
	if got[0].Host != "10.0.0.21:22" {
		t.Errorf("Host = %q, want 10.0.0.21:22", got[0].Host)
	}
	if len(got[0].Added) != 1 || got[0].Added[0] != "/dev/sdc" {
		t.Errorf("Added = %v, want [/dev/sdc]", got[0].Added)
	}
	if len(got[0].Removed) != 0 {
		t.Errorf("Removed = %v, want empty", got[0].Removed)
	}
}

func TestDetectDrift_diskRemoved(t *testing.T) {
	prev := []probe.HostFacts{host("10.0.0.21", "/dev/sdb", "/dev/sdc")}
	fresh := []probe.HostFacts{host("10.0.0.21", "/dev/sdb")}
	got := DetectDrift(prev, fresh)
	if len(got) != 1 {
		t.Fatalf("expected one drift report, got %+v", got)
	}
	if len(got[0].Removed) != 1 || got[0].Removed[0] != "/dev/sdc" {
		t.Errorf("Removed = %v, want [/dev/sdc]", got[0].Removed)
	}
}

func TestDetectDrift_replacedDisk(t *testing.T) {
	// Operator pulled /dev/sdc and added /dev/sdd. Both ends should
	// surface so the operator can confirm the swap was intentional.
	prev := []probe.HostFacts{host("10.0.0.21", "/dev/sdb", "/dev/sdc")}
	fresh := []probe.HostFacts{host("10.0.0.21", "/dev/sdb", "/dev/sdd")}
	got := DetectDrift(prev, fresh)
	if len(got) != 1 {
		t.Fatalf("expected one drift report, got %+v", got)
	}
	if len(got[0].Added) != 1 || got[0].Added[0] != "/dev/sdd" {
		t.Errorf("Added = %v, want [/dev/sdd]", got[0].Added)
	}
	if len(got[0].Removed) != 1 || got[0].Removed[0] != "/dev/sdc" {
		t.Errorf("Removed = %v, want [/dev/sdc]", got[0].Removed)
	}
}

func TestDetectDrift_newHostSkipped(t *testing.T) {
	// A host present in fresh but not prev is a new inventory addition,
	// not drift. The append-merge path will surface it; we shouldn't
	// also fire a drift warning on it.
	prev := []probe.HostFacts{host("10.0.0.21", "/dev/sdb")}
	fresh := []probe.HostFacts{
		host("10.0.0.21", "/dev/sdb"),
		host("10.0.0.22", "/dev/sdb", "/dev/sdc"),
	}
	if got := DetectDrift(prev, fresh); got != nil {
		t.Errorf("new host (no prev entry) should not surface as drift, got %+v", got)
	}
}

func TestDetectDrift_orphanHostSkipped(t *testing.T) {
	// A host present in prev but not in fresh is an orphan signal,
	// surfaced by MergeReport.Orphaned. DetectDrift stays out of that
	// lane: drift means "still here, different hardware", not "gone".
	prev := []probe.HostFacts{
		host("10.0.0.21", "/dev/sdb"),
		host("10.0.0.22", "/dev/sdb"),
	}
	fresh := []probe.HostFacts{host("10.0.0.21", "/dev/sdb")}
	if got := DetectDrift(prev, fresh); got != nil {
		t.Errorf("orphan host should not surface as drift, got %+v", got)
	}
}

func TestDetectDrift_multipleHostsSorted(t *testing.T) {
	// Output must be sorted by host so log lines / golden tests stay
	// stable across map iteration order.
	prev := []probe.HostFacts{
		host("10.0.0.22", "/dev/sdb"),
		host("10.0.0.21", "/dev/sdb"),
	}
	fresh := []probe.HostFacts{
		host("10.0.0.22", "/dev/sdb", "/dev/sdc"),
		host("10.0.0.21", "/dev/sdb", "/dev/sdc"),
	}
	got := DetectDrift(prev, fresh)
	if len(got) != 2 {
		t.Fatalf("expected two drift reports, got %+v", got)
	}
	if got[0].Host != "10.0.0.21:22" || got[1].Host != "10.0.0.22:22" {
		t.Errorf("hosts not sorted: got %s then %s", got[0].Host, got[1].Host)
	}
}

func TestDetectDrift_emptyPathsSkipped(t *testing.T) {
	// A probe entry with an empty Path (malformed lsblk row, very rare)
	// shouldn't manifest as a phantom drift entry.
	prev := []probe.HostFacts{{IP: "10.0.0.21", SSHPort: 22, Disks: []probe.DiskFact{{Path: "/dev/sdb"}, {Path: ""}}}}
	fresh := []probe.HostFacts{{IP: "10.0.0.21", SSHPort: 22, Disks: []probe.DiskFact{{Path: "/dev/sdb"}}}}
	if got := DetectDrift(prev, fresh); got != nil {
		t.Errorf("empty path in prev should not surface as drift, got %+v", got)
	}
}
