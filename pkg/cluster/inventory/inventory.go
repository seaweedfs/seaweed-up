// Package inventory defines the tiny "server list" format that feeds the
// probe and plan phases. See docs/design/inventory-and-plan.md for the full
// design. An inventory intentionally carries only what a planner cannot
// discover: host identity, SSH credentials, role assignment, and a few
// templating knobs. Everything else is probed or templated.
package inventory

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Recognized role names. The list mirrors the *_servers sections of
// spec.Specification, plus "external" for entries that are referenced from
// other sections (filer metadata stores, for example) but are not themselves
// SSH-managed or probed.
const (
	RoleMaster   = "master"
	RoleVolume   = "volume"
	RoleFiler    = "filer"
	RoleS3       = "s3"
	RoleSftp     = "sftp"
	RoleAdmin    = "admin"
	RoleWorker   = "worker"
	RoleEnvoy    = "envoy"
	RoleExternal = "external"
)

var validRoles = map[string]struct{}{
	RoleMaster:   {},
	RoleVolume:   {},
	RoleFiler:    {},
	RoleS3:       {},
	RoleSftp:     {},
	RoleAdmin:    {},
	RoleWorker:   {},
	RoleEnvoy:    {},
	RoleExternal: {},
}

// Inventory is the parsed form of an inventory.yaml file.
type Inventory struct {
	Defaults Defaults `yaml:"defaults"`
	Hosts    []Host   `yaml:"hosts"`
}

// Defaults apply to every host unless the host overrides them.
type Defaults struct {
	SSH  SSHConfig    `yaml:"ssh"`
	Disk DiskDefaults `yaml:"disk"`
}

// SSHConfig carries the bits needed to open an SSH connection.
type SSHConfig struct {
	User     string `yaml:"user,omitempty"`
	Port     int    `yaml:"port,omitempty"`
	Identity string `yaml:"identity,omitempty"`
}

// DiskDefaults are templating knobs for volume-host disk selection.
type DiskDefaults struct {
	// DeviceGlobs is the candidate set for auto-provisioned disks. Matches
	// lsblk device paths (e.g. "/dev/sd*", "/dev/nvme*"). When empty the
	// plan falls back to the same defaults used by prepareUnmountedDisks.
	DeviceGlobs []string `yaml:"device_globs,omitempty"`

	// Exclude is a per-host blacklist (e.g. the boot disk). Matched against
	// the same device-path form as DeviceGlobs.
	Exclude []string `yaml:"exclude,omitempty"`

	// ReservePct is the percentage of disk space to reserve for filesystem
	// overhead, capped at 10 GiB. Unset means the plan picks a default (5).
	ReservePct int `yaml:"reserve_pct,omitempty"`

	// DiskTypeAuto, when true, derives FolderSpec.DiskType from lsblk's
	// rotational bit: rotational → "hdd", otherwise "ssd".
	DiskTypeAuto bool `yaml:"disk_type_auto,omitempty"`
}

// Host is a single entry in the inventory's hosts list.
type Host struct {
	// IP is the host's management address. Keyed on for probe and merge.
	IP string `yaml:"ip"`

	// Roles is the set of *_servers sections this host appears in. A host
	// may appear in several; see docs/design/inventory-and-plan.md.
	Roles []string `yaml:"roles"`

	// Port is the service port for the single-role-instance this entry
	// represents. Zero means "use the role's default". Only meaningful for
	// roles that have a single service port; used to key multi-instance
	// volume servers on the same host.
	Port int `yaml:"port,omitempty"`

	// SSH overrides the inventory defaults for this host only.
	SSH *SSHConfig `yaml:"ssh,omitempty"`

	// Labels map onto DataCenter / Rack on the spec, and are preserved as
	// comment annotations for anything else the planner doesn't understand.
	Labels map[string]string `yaml:"labels,omitempty"`

	// Tag is a free-form symbolic name. Useful for external entries
	// referenced from flags (e.g. tag: postgres-metadata).
	Tag string `yaml:"tag,omitempty"`
}

// Load reads and validates an inventory file.
func Load(path string) (*Inventory, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read inventory %s: %w", path, err)
	}
	inv := &Inventory{}
	if err := yaml.Unmarshal(data, inv); err != nil {
		return nil, fmt.Errorf("parse inventory %s: %w", path, err)
	}
	if err := inv.Validate(); err != nil {
		return nil, err
	}
	return inv, nil
}

// Validate enforces the inventory invariants declared in the design doc.
// Duplicate ip:port entries within the same role are rejected; duplicate IPs
// across different ports (multi-instance) are allowed.
func (inv *Inventory) Validate() error {
	if len(inv.Hosts) == 0 {
		return fmt.Errorf("inventory has no hosts")
	}

	type key struct{ ip, role string; port int }
	seen := make(map[key]struct{})

	for i, h := range inv.Hosts {
		if h.IP == "" {
			return fmt.Errorf("host[%d] has no ip", i)
		}
		if len(h.Roles) == 0 {
			return fmt.Errorf("host %s has no roles", h.IP)
		}
		for _, role := range h.Roles {
			if _, ok := validRoles[role]; !ok {
				return fmt.Errorf("host %s has unknown role %q", h.IP, role)
			}
			k := key{ip: h.IP, role: role, port: h.Port}
			if _, dup := seen[k]; dup {
				return fmt.Errorf("host %s declares role %q at port %d twice", h.IP, role, h.Port)
			}
			seen[k] = struct{}{}
		}
	}
	return nil
}

// EffectiveSSH returns the SSH config for a host, merging per-host overrides
// on top of the inventory defaults. Missing port defaults to 22.
func (inv *Inventory) EffectiveSSH(h *Host) SSHConfig {
	out := inv.Defaults.SSH
	if h.SSH != nil {
		if h.SSH.User != "" {
			out.User = h.SSH.User
		}
		if h.SSH.Port != 0 {
			out.Port = h.SSH.Port
		}
		if h.SSH.Identity != "" {
			out.Identity = h.SSH.Identity
		}
	}
	if out.Port == 0 {
		out.Port = 22
	}
	return out
}

// HasRole reports whether h is tagged with role.
func (h *Host) HasRole(role string) bool {
	for _, r := range h.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// ProbeHosts returns the deduplicated set of hosts to SSH-probe. External-only
// hosts (e.g. a Postgres metadata store referenced by filers) are skipped.
// Multi-instance inventories (several role entries at the same IP but
// different service ports) share a single SSH target and therefore a single
// probe — keyed by ip:<ssh-port>. The planner later fans the single result
// back out to every role-instance entry for that host.
func (inv *Inventory) ProbeHosts() []*Host {
	var out []*Host
	seen := make(map[string]struct{})
	for i := range inv.Hosts {
		h := &inv.Hosts[i]
		if len(h.Roles) == 1 && h.Roles[0] == RoleExternal {
			continue
		}
		ssh := inv.EffectiveSSH(h)
		key := fmt.Sprintf("%s:%d", h.IP, ssh.Port)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, h)
	}
	return out
}
