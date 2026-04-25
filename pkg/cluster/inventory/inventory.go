// Package inventory defines the tiny "server list" format that feeds the
// probe and plan phases. See docs/design/inventory-and-plan.md for the full
// design. An inventory intentionally carries only what a planner cannot
// discover: host identity, SSH credentials, role assignment, and a few
// templating knobs. Everything else is probed or templated.
package inventory

import (
	"fmt"
	"os"
	"strings"

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
	// DeviceGlobs is the candidate set for auto-provisioned disks.
	// Matches lsblk device paths (e.g. "/dev/sd*", "/dev/nvme*",
	// "/dev/xvd*", "/dev/vd*"). When empty the plan falls back to the
	// defaults used by prepareUnmountedDisks: /dev/sd*, /dev/nvme*,
	// /dev/xvd* (Xen, older AWS), /dev/vd* (KVM virtio — Vultr,
	// Linode, Hetzner, OpenStack).
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

	// AllowEphemeral, when true, lets the planner emit folders for
	// disks the probe identified as cloud instance-store / ephemeral
	// (AWS Nitro instance store, GCP local SSD). Default is false:
	// ephemeral disks would lose all SeaweedFS data on stop/start, so
	// the planner skips them and surfaces them in
	// Report.EphemeralDisksSkipped instead. Operators who actually
	// want SeaweedFS on instance store (cache tier, scratch volume)
	// can flip this on.
	AllowEphemeral bool `yaml:"allow_ephemeral,omitempty"`

	// AutoIdxTier, when true, lets the planner carve a single small
	// fast disk out of each volume host's eligible disks and use it
	// as the `-dir.idx` mount (the `weed volume -dir.idx=…` flag,
	// equivalent to the helm chart's volume.idx field). The heuristic
	// fires only when the host has BOTH rotational and non-rotational
	// disks AND the smallest non-rotational disk is at most
	// IdxTierSizeRatio of the smallest rotational one — the
	// "small fast SSD + bulk HDDs" pattern. When fast disks are
	// comparable in size to slow disks, or when the host is all
	// uniform tier, no idx carve-out happens.
	AutoIdxTier bool `yaml:"auto_idx_tier,omitempty"`

	// IdxTierSizeRatio is the maximum ratio of (smallest fast disk) /
	// (smallest slow disk) at which auto_idx_tier fires. 0 falls back
	// to a built-in default (1/3). Set lower (e.g. 0.1) to require a
	// more pronounced size gap, higher (e.g. 0.5) to be more
	// permissive. Ignored when AutoIdxTier is false.
	IdxTierSizeRatio float64 `yaml:"idx_tier_size_ratio,omitempty"`
}

// Host is a single entry in the inventory's hosts list.
type Host struct {
	// IP is the host's management address. Keyed on for probe and merge.
	IP string `yaml:"ip"`

	// Roles is the set of *_servers sections this host appears in. A host
	// may appear in several; see docs/design/inventory-and-plan.md.
	Roles []string `yaml:"roles"`

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

// Validate enforces the inventory invariants declared in the design doc:
// every host has an IP and at least one known role, no (ip, role) pair
// appears twice, and no two rows sharing an ip:ssh-port target disagree
// on SSH credentials (otherwise the probe dedup would silently pick a
// winner). Disk-glob knobs are also sanity-checked up front so the
// probe isn't left silently matching nothing.
func (inv *Inventory) Validate() error {
	if len(inv.Hosts) == 0 {
		return fmt.Errorf("inventory has no hosts")
	}
	for _, g := range inv.Defaults.Disk.DeviceGlobs {
		if err := validateDeviceGlob(g); err != nil {
			return fmt.Errorf("defaults.disk.device_globs: %w", err)
		}
	}
	for _, g := range inv.Defaults.Disk.Exclude {
		if err := validateDeviceGlob(g); err != nil {
			return fmt.Errorf("defaults.disk.exclude: %w", err)
		}
	}

	type roleKey struct{ ip, role string }
	seenRoles := make(map[roleKey]struct{})

	type sshKey struct {
		ip   string
		port int
	}
	sshSeen := make(map[sshKey]SSHConfig)
	sshWhere := make(map[sshKey]string) // for the error message

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
			rk := roleKey{ip: h.IP, role: role}
			if _, dup := seenRoles[rk]; dup {
				return fmt.Errorf("host %s declares role %q twice", h.IP, role)
			}
			seenRoles[rk] = struct{}{}
		}

		// External entries don't open SSH sessions, so they don't need to
		// agree on SSH config with anyone else.
		if len(h.Roles) == 1 && h.Roles[0] == RoleExternal {
			continue
		}
		eff := inv.EffectiveSSH(&inv.Hosts[i])
		sk := sshKey{ip: h.IP, port: eff.Port}
		if prev, ok := sshSeen[sk]; ok {
			if prev != eff {
				return fmt.Errorf(
					"host %s at ssh port %d has conflicting ssh config: host[%d] declares user=%q identity=%q, but earlier row %s declares user=%q identity=%q",
					h.IP, eff.Port,
					i, eff.User, eff.Identity,
					sshWhere[sk], prev.User, prev.Identity,
				)
			}
		} else {
			sshSeen[sk] = eff
			sshWhere[sk] = fmt.Sprintf("host[%d]", i)
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

// validateDeviceGlob enforces the (limited) glob vocabulary we support for
// device-path patterns: a literal path with an optional trailing '*'.
// Anything fancier (character classes, leading or interior wildcards) would
// silently match nothing once we convert to a prefix below, so reject it
// here where the operator can still fix the inventory.
func validateDeviceGlob(g string) error {
	if g == "" {
		return fmt.Errorf("empty glob")
	}
	trimmed := strings.TrimSuffix(g, "*")
	if strings.ContainsAny(trimmed, "*?[]{}") {
		return fmt.Errorf("unsupported glob %q: only a literal path with an optional trailing '*' is allowed", g)
	}
	return nil
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
