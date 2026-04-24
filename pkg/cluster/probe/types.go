// Package probe SSHes into hosts listed in an inventory and collects
// hardware facts (CPU, memory, disks, network, OS). The output is
// consumed by `cluster probe --json` for scripting and by the future
// `cluster plan` command as input for synthesizing a cluster.yaml.
//
// Phase 1 scope (see docs/design/inventory-and-plan.md): collection only.
// No synthesis, no YAML emission.
package probe

import "time"

// HostFacts is the full per-host probe result. Fields are best-effort:
// if a single sub-probe fails, the corresponding field is left at its
// zero value and probing continues. A full SSH failure sets ProbeError
// and leaves the rest zero.
type HostFacts struct {
	IP          string     `json:"ip"`
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
type DiskFact struct {
	Path       string `json:"path"`
	Size       uint64 `json:"size_bytes"`
	FSType     string `json:"fstype,omitempty"`
	UUID       string `json:"uuid,omitempty"`
	MountPoint string `json:"mountpoint,omitempty"`
	Rotational bool   `json:"rotational"`
	Model      string `json:"model,omitempty"`
}

// NetIface is one non-loopback network interface. SpeedMbps is 0 when
// the kernel reports no value (virtual NICs, some hypervisors).
type NetIface struct {
	Name      string   `json:"name"`
	Addresses []string `json:"addresses,omitempty"`
	SpeedMbps int      `json:"speed_mbps,omitempty"`
}
