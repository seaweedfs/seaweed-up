// Package probe SSHes into hosts listed in an inventory and collects
// hardware facts (CPU, memory, disks, network, OS). The output feeds
// `cluster plan` — in Phase 1 it is emitted directly as JSON; in
// Phase 2 it will feed the cluster.yaml synthesis step.
//
// Phase 1 scope (see docs/design/inventory-and-plan.md): collection only.
// No synthesis, no YAML emission.
package probe

import "time"

// HostFacts is the full per-host probe result. Fields are best-effort:
// if a single sub-probe fails, the corresponding field is left at its
// zero value and probing continues. A full SSH failure sets ProbeError
// and leaves the rest zero.
//
// The (IP, SSHPort) pair identifies which SSH target the facts came
// from. Inventories that run multiple role-instances on the same host
// (same IP, several service ports) dedup at the SSH-target level — see
// Inventory.ProbeHosts — so every HostFacts maps to exactly one SSH
// session. Downstream consumers key results back to inventory entries
// by matching (IP, SSHPort).
type HostFacts struct {
	IP          string     `json:"ip"`
	SSHPort     int        `json:"ssh_port,omitempty"`
	Hostname    string     `json:"hostname,omitempty"`
	OS          string     `json:"os,omitempty"`         // e.g. "ubuntu"
	OSVersion   string     `json:"os_version,omitempty"` // e.g. "22.04"
	Arch        string     `json:"arch,omitempty"`       // e.g. "amd64"
	CPUCores    int        `json:"cpu_cores,omitempty"`
	MemoryBytes uint64     `json:"memory_bytes,omitempty"`
	NetIfaces   []NetIface `json:"net_ifaces,omitempty"`
	Disks       []DiskFact `json:"disks,omitempty"`
	ProbedAt    time.Time  `json:"probed_at"`
	ProbeError  string     `json:"probe_error,omitempty"`
}

// DiskFact is one entry from lsblk, filtered by the inventory's device
// globs. Only fields useful to the planner or to an operator reviewing
// the probe output are kept.
//
// Rotational is a pointer so we can represent three states: true
// (spinning HDD), false (SSD/NVMe), and nil (lsblk's ROTA column was
// empty — common for virtio, loop, and some device-mapper nodes).
// JSON rendering: `true`, `false`, or the field is omitted entirely.
// Downstream consumers MUST treat a missing/null value as "unknown"
// and fall back to an explicit `disk_type` in inventory rather than
// silently picking one.
type DiskFact struct {
	Path       string `json:"path"`
	Size       uint64 `json:"size_bytes"`
	FSType     string `json:"fstype,omitempty"`
	UUID       string `json:"uuid,omitempty"`
	MountPoint string `json:"mountpoint,omitempty"`
	Rotational *bool  `json:"rotational,omitempty"`
	Model      string `json:"model,omitempty"`
}

// NetIface is one non-loopback network interface. SpeedMbps is 0 when
// the kernel reports no value (virtual NICs, some hypervisors).
type NetIface struct {
	Name      string   `json:"name"`
	Addresses []string `json:"addresses,omitempty"`
	SpeedMbps int      `json:"speed_mbps,omitempty"`
}
