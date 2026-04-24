// Package plan synthesizes a reviewable cluster.yaml (a
// spec.Specification) from an inventory plus per-host probe facts. This
// is Phase 2 of the inventory → plan → deploy flow described in
// docs/design/inventory-and-plan.md.
//
// Greenfield only. Phase 3 will add append-merge via yaml.Node so
// re-running against an existing cluster.yaml preserves comments and
// hand edits. Until then, writing onto an existing file requires
// --force.
package plan

import (
	"fmt"
	"net/netip"
	"sort"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/inventory"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/probe"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

// Well-known default service ports. These mirror the addToBufferInt
// fallbacks used by the WriteToBuffer methods on each *ServerSpec. The
// planner emits ports explicitly so the generated cluster.yaml is
// self-describing and doesn't rely on struct-tag defaults, which the
// codebase does not actually apply at load time (no defaults library
// is imported).
//
// gRPC ports follow SeaweedFS's convention of "service port + 10000"
// (see the addToBufferInt calls in each WriteToBuffer), with an explicit
// fallback for admin which has no gRPC port.
const (
	DefaultMasterPort     = 9333
	DefaultMasterGrpcPort = 19333
	DefaultVolumePort     = 8080
	DefaultVolumeGrpcPort = 18080
	DefaultFilerPort      = 8888
	DefaultFilerGrpcPort  = 18888
	DefaultS3Port         = 8333
	DefaultSftpPort       = 2022
	DefaultAdminPort      = 23646
)

// Options knobs that influence synthesis. All optional; zero values are
// fine and trigger sensible defaults.
type Options struct {
	// ClusterName populates Specification.Name.
	ClusterName string

	// VolumeSizeLimitMB is the max size of a single volume file. Used as
	// the divisor in the FolderSpec.Max formula. If zero, falls back to
	// GlobalOptions' default (5000).
	VolumeSizeLimitMB int

	// FilerBackend, when non-nil, is applied as Config on every emitted
	// FilerServerSpec. Callers supply this by parsing a DSN via
	// ParseFilerBackendDSN.
	FilerBackend map[string]interface{}
}

// Generate turns the inventory + probed facts into a Specification.
// Pure function — no I/O, no side effects. Hosts that declare no SSH
// roles (role: external only) are passed through to the role-entry
// generation but contribute no entries; their IP may still be referenced
// elsewhere (e.g. a filer backend DSN).
//
// factsByTarget is keyed by "<ip>:<ssh-port>" so callers can produce it
// from probe.HostFacts directly.
func Generate(inv *inventory.Inventory, factsByTarget map[string]probe.HostFacts, opts Options) (*spec.Specification, error) {
	if inv == nil {
		return nil, fmt.Errorf("inventory is nil")
	}
	if factsByTarget == nil {
		factsByTarget = map[string]probe.HostFacts{}
	}

	volumeSizeLimitMB := opts.VolumeSizeLimitMB
	if volumeSizeLimitMB == 0 {
		volumeSizeLimitMB = 5000 // GlobalOptions default
	}

	out := &spec.Specification{
		Name: opts.ClusterName,
		GlobalOptions: spec.GlobalOptions{
			ConfigDir:         "/etc/seaweed",
			DataDir:           "/opt/seaweed",
			VolumeSizeLimitMB: volumeSizeLimitMB,
			Replication:       "000",
		},
	}

	// Process hosts in inventory order so the output is stable and
	// comparable to golden files. Fan each host out to one entry per
	// role it declares.
	for i := range inv.Hosts {
		h := &inv.Hosts[i]
		ssh := inv.EffectiveSSH(h)
		target := sshTargetKey(h.IP, ssh.Port)
		facts := factsByTarget[target]

		for _, role := range h.Roles {
			switch role {
			case inventory.RoleExternal:
				// External hosts are pure references — they don't
				// land in any *_servers section. A later flag could
				// lift `tag:` into a DSN rewrite target, but that's
				// Phase 4 ergonomics.
				continue
			case inventory.RoleMaster:
				out.MasterServers = append(out.MasterServers, newMasterSpec(h, ssh))
			case inventory.RoleVolume:
				vol, err := newVolumeSpec(h, ssh, facts, inv.Defaults.Disk, volumeSizeLimitMB)
				if err != nil {
					return nil, fmt.Errorf("volume host %s: %w", h.IP, err)
				}
				out.VolumeServers = append(out.VolumeServers, vol)
			case inventory.RoleFiler:
				out.FilerServers = append(out.FilerServers, newFilerSpec(h, ssh, opts.FilerBackend))
			case inventory.RoleS3:
				out.S3Servers = append(out.S3Servers, newS3Spec(h, ssh))
			case inventory.RoleSftp:
				out.SftpServers = append(out.SftpServers, newSftpSpec(h, ssh))
			case inventory.RoleAdmin:
				out.AdminServers = append(out.AdminServers, newAdminSpec(h, ssh))
			case inventory.RoleWorker:
				out.WorkerServers = append(out.WorkerServers, newWorkerSpec(h, ssh))
			case inventory.RoleEnvoy:
				out.EnvoyServers = append(out.EnvoyServers, newEnvoySpec(h, ssh))
			default:
				return nil, fmt.Errorf("unknown role %q on host %s", role, h.IP)
			}
		}
	}

	// Post-process: wire S3 and SFTP gateways to the first filer if not
	// overridden. Matches the convention in examples/typical.yaml and
	// avoids failing validation in the filer-prerequisite check.
	if len(out.FilerServers) > 0 {
		defaultFiler := fmt.Sprintf("%s:%d", out.FilerServers[0].Ip, DefaultFilerPort)
		for _, s := range out.S3Servers {
			if s.Filer == "" {
				s.Filer = defaultFiler
			}
		}
		for _, s := range out.SftpServers {
			if s.Filer == "" {
				s.Filer = defaultFiler
			}
		}
	}

	// Workers point at the first admin server when no explicit admin is
	// set. Matches the precedence in resolveWorkerDefaultAdmins.
	if len(out.AdminServers) > 0 {
		defaultAdmin := fmt.Sprintf("%s:%d", out.AdminServers[0].Ip, DefaultAdminPort)
		for _, w := range out.WorkerServers {
			if w.Admin == "" {
				w.Admin = defaultAdmin
			}
		}
	}

	return out, nil
}

func sshTargetKey(ip string, port int) string {
	return fmt.Sprintf("%s:%d", ip, port)
}

// --- per-role constructors ------------------------------------------------

func newMasterSpec(h *inventory.Host, ssh inventory.SSHConfig) *spec.MasterServerSpec {
	return &spec.MasterServerSpec{
		Ip:       h.IP,
		PortSsh:  ssh.Port,
		Port:     DefaultMasterPort,
		PortGrpc: DefaultMasterGrpcPort,
	}
}

func newVolumeSpec(h *inventory.Host, ssh inventory.SSHConfig, facts probe.HostFacts, disk inventory.DiskDefaults, volumeSizeLimitMB int) (*spec.VolumeServerSpec, error) {
	v := &spec.VolumeServerSpec{
		Ip:         h.IP,
		PortSsh:    ssh.Port,
		Port:       DefaultVolumePort,
		PortGrpc:   DefaultVolumeGrpcPort,
		DataCenter: h.Labels["zone"],
		Rack:       h.Labels["rack"],
	}
	v.Folders = deriveFolders(facts, disk, volumeSizeLimitMB)
	return v, nil
}

func newFilerSpec(h *inventory.Host, ssh inventory.SSHConfig, backend map[string]interface{}) *spec.FilerServerSpec {
	f := &spec.FilerServerSpec{
		Ip:         h.IP,
		PortSsh:    ssh.Port,
		Port:       DefaultFilerPort,
		PortGrpc:   DefaultFilerGrpcPort,
		DataCenter: h.Labels["zone"],
		Rack:       h.Labels["rack"],
	}
	if backend != nil {
		// Copy so callers can mutate the source map without mutating
		// the emitted spec.
		f.Config = make(map[string]interface{}, len(backend))
		for k, v := range backend {
			f.Config[k] = v
		}
	}
	return f
}

func newS3Spec(h *inventory.Host, ssh inventory.SSHConfig) *spec.S3ServerSpec {
	return &spec.S3ServerSpec{
		Ip:      h.IP,
		PortSsh: ssh.Port,
		Port:    DefaultS3Port,
	}
}

func newSftpSpec(h *inventory.Host, ssh inventory.SSHConfig) *spec.SftpServerSpec {
	return &spec.SftpServerSpec{
		Ip:      h.IP,
		PortSsh: ssh.Port,
		Port:    DefaultSftpPort,
	}
}

func newAdminSpec(h *inventory.Host, ssh inventory.SSHConfig) *spec.AdminServerSpec {
	return &spec.AdminServerSpec{
		Ip:      h.IP,
		PortSsh: ssh.Port,
		Port:    DefaultAdminPort,
	}
}

func newWorkerSpec(h *inventory.Host, ssh inventory.SSHConfig) *spec.WorkerServerSpec {
	return &spec.WorkerServerSpec{
		Ip:      h.IP,
		PortSsh: ssh.Port,
	}
}

func newEnvoySpec(h *inventory.Host, ssh inventory.SSHConfig) *spec.EnvoyServerSpec {
	return &spec.EnvoyServerSpec{
		Ip:      h.IP,
		PortSsh: ssh.Port,
	}
}

// --- disk layout derivation ----------------------------------------------

// deriveFolders maps probed disks onto FolderSpec entries per the rules
// in the design doc. Eligibility mirrors prepareUnmountedDisks: skip
// excluded paths, partitioned disks, and anything already mounted. For
// each eligible disk, emit a /data<N> folder and compute Max from usable
// size (after reserve_pct headroom, capped at 10 GiB).
//
// When the host has no eligible disks the result is an empty slice —
// the planner leaves a clearly wrong volume_server entry behind so
// `cluster deploy` fails validation, rather than silently dropping the
// host.
func deriveFolders(facts probe.HostFacts, disk inventory.DiskDefaults, volumeSizeLimitMB int) []*spec.FolderSpec {
	reservePct := disk.ReservePct
	if reservePct == 0 {
		reservePct = 5 // documented default
	}

	excluded := make(map[string]struct{}, len(disk.Exclude))
	for _, e := range disk.Exclude {
		// Exclude list was validated as glob-with-trailing-star or
		// literal (see inventory.validateDeviceGlob). Strip trailing *
		// to get the prefix.
		if len(e) > 0 && e[len(e)-1] == '*' {
			excluded[e[:len(e)-1]] = struct{}{}
		} else {
			excluded[e] = struct{}{}
		}
	}

	// Stable order: process facts in insertion order from the probe,
	// but pick /data<N> slots starting from 1 regardless of lsblk's
	// ordering. The probe already filtered by the defaults.disk.device_globs
	// prefix.
	var eligible []probe.DiskFact
	for _, d := range facts.Disks {
		if d.MountPoint != "" {
			continue // already mounted; probably not ours to reformat
		}
		if d.FSType != "" {
			continue // existing filesystem we didn't create
		}
		if isExcluded(d.Path, excluded) {
			continue
		}
		eligible = append(eligible, d)
	}
	// Sort by device path so the output is deterministic even if the
	// probe returned in a different order from run to run.
	sort.Slice(eligible, func(i, j int) bool {
		return eligible[i].Path < eligible[j].Path
	})

	folders := make([]*spec.FolderSpec, 0, len(eligible))
	for i, d := range eligible {
		folders = append(folders, &spec.FolderSpec{
			Folder:   fmt.Sprintf("/data%d", i+1),
			DiskType: diskTypeFor(d, disk),
			Max:      computeMax(d.Size, reservePct, volumeSizeLimitMB),
		})
	}
	return folders
}

func isExcluded(path string, excluded map[string]struct{}) bool {
	if _, ok := excluded[path]; ok {
		return true // literal match
	}
	for prefix := range excluded {
		if prefix != "" && len(path) >= len(prefix) && path[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// diskTypeFor picks "hdd" or "ssd". When Rotational is nil (lsblk
// couldn't tell) and auto-detect is on, we conservatively emit "hdd":
// treating an unknown disk as SSD would set misleading expectations
// in the generated YAML. The operator can hand-edit before deploy.
func diskTypeFor(d probe.DiskFact, disk inventory.DiskDefaults) string {
	if !disk.DiskTypeAuto {
		return "hdd"
	}
	if d.Rotational == nil {
		return "hdd"
	}
	if *d.Rotational {
		return "hdd"
	}
	return "ssd"
}

// computeMax is the volume-count cap for a folder:
//
//	sizeMiB    = size / (1024 * 1024)
//	reserveMiB = min(sizeMiB * reserve_pct / 100, 10 * 1024)
//	usableMiB  = sizeMiB - reserveMiB
//	max        = usableMiB / volumeSizeLimitMB   (integer)
//
// Returns 0 when the disk can't host a single volume at the requested
// limit. The deploy path treats max=0 as unlimited, so an explicit
// floor would be misleading; operators can hand-edit if needed.
func computeMax(sizeBytes uint64, reservePct, volumeSizeLimitMB int) int {
	if volumeSizeLimitMB <= 0 {
		return 0
	}
	const maxReserveMiB = 10 * 1024
	sizeMiB := sizeBytes / (1024 * 1024)
	reserveMiB := sizeMiB * uint64(reservePct) / 100
	if reserveMiB > maxReserveMiB {
		reserveMiB = maxReserveMiB
	}
	if reserveMiB > sizeMiB {
		return 0
	}
	usableMiB := sizeMiB - reserveMiB
	return int(usableMiB / uint64(volumeSizeLimitMB))
}

// --- identity helpers ----------------------------------------------------

// ValidateHostIP returns an error when h.IP isn't a parseable IPv4 /
// IPv6 literal. The inventory loader already rejects empty IP, but a
// typo like "10.0.01" passes that check and then quietly breaks the
// generated YAML. netip.ParseAddr covers both families in one call.
func ValidateHostIP(h *inventory.Host) error {
	if _, err := netip.ParseAddr(h.IP); err != nil {
		return fmt.Errorf("host %s: %w", h.IP, err)
	}
	return nil
}
