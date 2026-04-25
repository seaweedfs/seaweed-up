package plan

import (
	"fmt"
	"sort"
	"strings"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/inventory"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/probe"
)

// DeployDiskAllowlist captures the set of block-device paths the
// planner approves for `cluster deploy`'s prepareUnmountedDisks step,
// keyed by SSH target ("<ip>:<ssh-port>"). This is the explicit,
// canonical record of plan's classification — including the
// inventory's defaults.disk.exclude rules and the per-disk ephemeral
// skip — so deploy doesn't have to re-derive (and risk diverging
// from) the same logic by re-reading raw probe facts.
//
// Disks with a foreign mount, an existing filesystem without an
// fstab/cluster claim, or matching the exclude list are deliberately
// omitted: those are off-limits for deploy regardless of how plan
// produced cluster.yaml.
type DeployDiskAllowlist map[string][]string

// EligibleDisks computes the per-target allowlist that mirrors the
// classification done by deriveFolders. Returns SSH-target keys
// (`<ip>:<ssh-port>`) so the result composes with
// inventory.ProbeHosts dedup output and the manager's per-host
// sync.Once gate.
//
// The returned slice for each host is path-sorted for deterministic
// JSON output and golden-file comparisons. Targets that contribute no
// eligible disks are omitted from the map.
func EligibleDisks(inv *inventory.Inventory, factsByTarget map[string]probe.HostFacts) DeployDiskAllowlist {
	out := DeployDiskAllowlist{}
	if inv == nil {
		return out
	}
	for i := range inv.Hosts {
		h := &inv.Hosts[i]
		// External hosts never get probed and never participate in
		// disk provisioning.
		if len(h.Roles) == 1 && h.Roles[0] == inventory.RoleExternal {
			continue
		}
		if !h.HasRole(inventory.RoleVolume) {
			continue
		}
		ssh := inv.EffectiveSSH(h)
		target := fmt.Sprintf("%s:%d", h.IP, ssh.Port)
		facts, ok := factsByTarget[target]
		if !ok || facts.ProbeError != "" {
			continue
		}
		paths := classifyEligibleDiskPaths(facts.Disks, inv.Defaults.Disk)
		if len(paths) > 0 {
			out[target] = paths
		}
	}
	return out
}

// classifyEligibleDiskPaths walks a host's probed disks and returns
// the paths plan would approve for deploy: ephemerals are skipped
// (unless allow_ephemeral), inventory excludes apply, foreign mounts
// are dropped, and fs-without-claim is dropped. Mirrors the
// classification matrix in deriveFolders so plan and deploy agree.
func classifyEligibleDiskPaths(input []probe.DiskFact, disk inventory.DiskDefaults) []string {
	literalExcluded := make(map[string]struct{}, len(disk.Exclude))
	var prefixExcluded []string
	for _, e := range disk.Exclude {
		if len(e) > 0 && e[len(e)-1] == '*' {
			prefixExcluded = append(prefixExcluded, e[:len(e)-1])
		} else {
			literalExcluded[e] = struct{}{}
		}
	}

	var out []string
	for _, d := range input {
		if d.Ephemeral && !disk.AllowEphemeral {
			continue
		}
		if isExcluded(d.Path, literalExcluded, prefixExcluded) {
			continue
		}
		effective := d.MountPoint
		if effective == "" {
			effective = d.FstabMountPoint
		}
		if effective != "" {
			// Cluster-claimed /dataN mounts are eligible (re-deploy
			// idempotent); foreign mounts are not.
			if _, ok := parseClusterDataMount(effective); !ok {
				continue
			}
			out = append(out, d.Path)
			continue
		}
		if d.FSType != "" {
			// Filesystem with no claim — not ours to touch.
			continue
		}
		out = append(out, d.Path)
	}
	sort.Strings(out)
	return out
}

// SSHTargetKey computes the same `<ip>:<ssh-port>` key the planner uses
// in its allowlist. Exposed so deploy can build the lookup key from a
// VolumeServerSpec consistently.
func SSHTargetKey(ip string, sshPort int) string {
	return fmt.Sprintf("%s:%d", ip, sshPort)
}

// SplitSSHTargetKey is the inverse of SSHTargetKey, returning the IP
// portion. Used by deploy when iterating an allowlist that needs to
// match against a host that the manager only knows by IP. Robust to
// IPv6 addresses (last colon = port separator).
func SplitSSHTargetKey(key string) (ip string, port string) {
	idx := strings.LastIndex(key, ":")
	if idx < 0 {
		return key, ""
	}
	return key[:idx], key[idx+1:]
}
