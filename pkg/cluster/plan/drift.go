package plan

import (
	"sort"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/probe"
)

// DriftReport summarizes per-host hardware changes between a previous
// plan run's recorded facts and the current probe. The cmd layer
// surfaces these as `WARN: drift` lines so operators see hosts whose
// hardware shape has shifted since the last plan — typically a disk
// added or removed without anyone re-running plan in between.
//
// Empty (Added + Removed both empty) means "no observed drift on
// this host"; DetectDrift omits such reports entirely.
type DriftReport struct {
	// Host is the SSH target (ip:port) the drift was observed on.
	// Matches the same key inventory.ProbeHosts and the rest of plan
	// use, so log lines line up across messages.
	Host string
	// Added lists disk paths the current probe sees but the previous
	// facts.json didn't (operator added a drive between plan runs).
	Added []string
	// Removed lists disk paths the previous facts.json had but the
	// current probe doesn't see (operator pulled a drive, the kernel
	// stopped enumerating it, or it failed entirely).
	Removed []string
}

// IsEmpty reports whether the report contains any observed change.
func (d DriftReport) IsEmpty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0
}

// DetectDrift compares prev (loaded from cluster.facts.json) against
// fresh (the current probe's HostFacts slice) and returns one
// DriftReport per host whose disk path set has changed.
//
// Hosts present in fresh but not in prev are skipped — they're new
// inventory additions, not drift. Hosts present in prev but not in
// fresh are skipped too: the orphan signal is the cluster.yaml
// MergeReport.Orphaned warning, not drift (drift would imply the
// host is still here but with different hardware).
//
// Currently the comparison covers disk path set only. Size changes,
// model strings, NIC counts, and CPU/memory shifts are deliberately
// out of scope — they're either too noisy on cloud hosts (NICs
// detach/reattach across reboots; disks get resized in place) or
// don't move the needle on a deploy decision. Future expansion can
// add more dimensions one field at a time as use cases emerge.
func DetectDrift(prev, fresh []probe.HostFacts) []DriftReport {
	if len(prev) == 0 || len(fresh) == 0 {
		return nil
	}
	prevByTarget := make(map[string]probe.HostFacts, len(prev))
	for _, p := range prev {
		prevByTarget[SSHTargetKey(p.IP, p.SSHPort)] = p
	}
	var out []DriftReport
	for _, f := range fresh {
		key := SSHTargetKey(f.IP, f.SSHPort)
		p, ok := prevByTarget[key]
		if !ok {
			continue
		}
		if r := compareDiskPaths(key, p, f); !r.IsEmpty() {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out
}

// compareDiskPaths builds a DriftReport from the symmetric difference
// of prev.Disks vs fresh.Disks (keyed on Path). Empty paths are
// skipped so a malformed probe entry doesn't manifest as drift.
func compareDiskPaths(host string, prev, fresh probe.HostFacts) DriftReport {
	prevPaths := pathSet(prev.Disks)
	freshPaths := pathSet(fresh.Disks)
	report := DriftReport{Host: host}
	for p := range freshPaths {
		if _, ok := prevPaths[p]; !ok {
			report.Added = append(report.Added, p)
		}
	}
	for p := range prevPaths {
		if _, ok := freshPaths[p]; !ok {
			report.Removed = append(report.Removed, p)
		}
	}
	sort.Strings(report.Added)
	sort.Strings(report.Removed)
	return report
}

func pathSet(disks []probe.DiskFact) map[string]struct{} {
	out := make(map[string]struct{}, len(disks))
	for _, d := range disks {
		if d.Path != "" {
			out[d.Path] = struct{}{}
		}
	}
	return out
}
