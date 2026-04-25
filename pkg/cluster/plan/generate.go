// Package plan synthesizes a reviewable cluster.yaml (a
// spec.Specification) from an inventory plus per-host probe facts. This
// is Phase 2 of the inventory → plan → deploy flow described in
// docs/design/inventory-and-plan.md.
//
// Greenfield only. Phase 3 will add append-merge via yaml.Node so
// re-running against an existing cluster.yaml preserves comments and
// hand edits. Until then, writing onto an existing file requires
// --overwrite.
package plan

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"

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

// Volume shapes describe how `plan` lays multiple eligible disks on a
// host into volume_server entries. The CLI exposes these as a
// `--volume-server-shape` enum so future grouping rules (per-rack,
// per-numa-node, ...) can land without a new flag.
const (
	// VolumeServerShapePerHost — one volume_server per host, all eligible
	// disks listed under its folders:. The simpler default. Matches
	// the typical example in examples/typical.yaml.
	VolumeServerShapePerHost = "per-host"

	// VolumeServerShapePerDisk — one volume_server per eligible disk on a
	// host. Each entry carries exactly one folder and a distinct port
	// (DefaultVolumePort + index). Matches the helm chart's "1 process
	// per disk" replicas pattern; gives fault isolation (a single
	// volume process crash doesn't take down sibling disks on the
	// same host).
	VolumeServerShapePerDisk = "per-disk"
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

	// VolumeServerShape controls how multiple disks on one host are mapped
	// onto volume_server entries. "" or VolumeServerShapePerHost emits one
	// volume_server with all folders; VolumeServerShapePerDisk emits one
	// per disk with distinct ports. See the constants above.
	VolumeServerShape string
}

// Report collects hosts/roles that Generate chose to skip so the CLI can
// surface them to the operator. Zero-value (no skips) is the happy path.
type Report struct {
	// ProbeFailed lists hosts dropped entirely because their probe set
	// HostFacts.ProbeError. These hosts contribute no entries to any
	// *_servers section. The inventory can be re-run against plan once
	// the host is reachable.
	ProbeFailed []ProbeFailure

	// VolumeHostsNoDisks lists hosts whose `volume` role was dropped
	// because no eligible free disks were discovered on them. Other
	// roles on the same host (master, filer, ...) are still emitted.
	VolumeHostsNoDisks []string

	// EphemeralDisksSkipped lists ephemeral / instance-store disks
	// that the planner refused to provision (AWS Nitro instance store,
	// GCP local SSD). Set defaults.disk.allow_ephemeral on the
	// inventory to opt in.
	EphemeralDisksSkipped []EphemeralSkip
}

// ProbeFailure is a single skipped host + the reason.
type ProbeFailure struct {
	IP     string
	Reason string
}

// EphemeralSkip is the per-host list of ephemeral disk paths that the
// planner refused to include because defaults.disk.allow_ephemeral was
// false.
type EphemeralSkip struct {
	IP    string
	Disks []string
}

// Generate turns the inventory + probed facts into a Specification plus
// a Report describing hosts/roles that had to be skipped. Pure function —
// no I/O, no side effects. Hosts in the `external` role contribute no
// entries.
//
// Skip rules (per docs/design/inventory-and-plan.md edge-case table):
//   - A host whose probe failed (HostFacts.ProbeError != "") is skipped
//     entirely. All of its roles are dropped and the host is listed in
//     Report.ProbeFailed. Emitting an entry for an unreachable host would
//     produce a plan that looks deployable but fails only at deploy.
//   - A host tagged `volume` whose probe returned no eligible disks is
//     dropped from volume_servers only. Other roles on the same host
//     still emit entries. The host is listed in Report.VolumeHostsNoDisks.
//     An empty folders: list would start a volume server with no data
//     dirs, which is worse than refusing to write one.
//
// factsByTarget is keyed by "<ip>:<ssh-port>" so callers can produce it
// from probe.HostFacts directly.
func Generate(inv *inventory.Inventory, factsByTarget map[string]probe.HostFacts, opts Options) (*spec.Specification, Report, error) {
	var report Report
	if inv == nil {
		return nil, report, fmt.Errorf("inventory is nil")
	}
	if factsByTarget == nil {
		factsByTarget = map[string]probe.HostFacts{}
	}

	volumeSizeLimitMB := opts.VolumeSizeLimitMB
	if volumeSizeLimitMB == 0 {
		volumeSizeLimitMB = 5000 // GlobalOptions default
	}

	switch opts.VolumeServerShape {
	case "", VolumeServerShapePerHost, VolumeServerShapePerDisk:
		// supported
	default:
		return nil, report, fmt.Errorf("unknown volume_server_shape %q; supported: %q, %q", opts.VolumeServerShape, VolumeServerShapePerHost, VolumeServerShapePerDisk)
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

		// Skip the whole host if its probe failed. Emitting partial
		// entries for unreachable hosts produces a cluster.yaml that
		// looks usable but only fails at deploy time.
		if facts.ProbeError != "" {
			report.ProbeFailed = append(report.ProbeFailed, ProbeFailure{IP: h.IP, Reason: facts.ProbeError})
			continue
		}

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
				eligibleFacts := facts
				if !inv.Defaults.Disk.AllowEphemeral {
					var kept []probe.DiskFact
					var skipped []string
					for _, d := range facts.Disks {
						if d.Ephemeral {
							skipped = append(skipped, d.Path)
							continue
						}
						kept = append(kept, d)
					}
					if len(skipped) > 0 {
						report.EphemeralDisksSkipped = append(report.EphemeralDisksSkipped, EphemeralSkip{
							IP:    h.IP,
							Disks: skipped,
						})
					}
					eligibleFacts.Disks = kept
				}
				folders := deriveFolders(eligibleFacts, inv.Defaults.Disk, volumeSizeLimitMB)
				if len(folders) == 0 {
					// Emitting a volume_server entry with no folders
					// would start `weed volume` without -dir, which
					// silently runs against the working directory
					// rather than the intended data disks. Drop the
					// role and warn loudly.
					report.VolumeHostsNoDisks = append(report.VolumeHostsNoDisks, h.IP)
					continue
				}
				if opts.VolumeServerShape == VolumeServerShapePerDisk {
					// Fan each folder out to its own volume_server. Port
					// increments per disk so the processes don't collide
					// on the same host (8080, 8081, 8082, ...). gRPC
					// follows SeaweedFS convention of "service port +
					// 10000".
					for i, folder := range folders {
						port := DefaultVolumePort + i
						vs := newVolumeSpec(h, ssh, []*spec.FolderSpec{folder})
						vs.Port = port
						vs.PortGrpc = port + 10000
						out.VolumeServers = append(out.VolumeServers, vs)
					}
					continue
				}
				out.VolumeServers = append(out.VolumeServers, newVolumeSpec(h, ssh, folders))
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
				return nil, report, fmt.Errorf("unknown role %q on host %s", role, h.IP)
			}
		}
	}

	// Post-process: wire S3 and SFTP gateways to the first filer if not
	// overridden. Matches the convention in examples/typical.yaml and
	// avoids failing validation in the filer-prerequisite check.
	// net.JoinHostPort handles IPv6 literals (wraps in brackets).
	if len(out.FilerServers) > 0 {
		defaultFiler := net.JoinHostPort(out.FilerServers[0].Ip, strconv.Itoa(DefaultFilerPort))
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
		defaultAdmin := net.JoinHostPort(out.AdminServers[0].Ip, strconv.Itoa(DefaultAdminPort))
		for _, w := range out.WorkerServers {
			if w.Admin == "" {
				w.Admin = defaultAdmin
			}
		}
	}

	return out, report, nil
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

func newVolumeSpec(h *inventory.Host, ssh inventory.SSHConfig, folders []*spec.FolderSpec) *spec.VolumeServerSpec {
	return &spec.VolumeServerSpec{
		Ip:         h.IP,
		PortSsh:    ssh.Port,
		Port:       DefaultVolumePort,
		PortGrpc:   DefaultVolumeGrpcPort,
		DataCenter: h.Labels["zone"],
		Rack:       h.Labels["rack"],
		Folders:    folders,
	}
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

// AdminPasswordPlaceholder marks the admin_password field as unfilled.
// Callers (Marshal, the CLI summary) detect this value and tell the
// operator to substitute a real secret before deploy. Kept in sync with
// the CHANGE_ME convention in examples/typical.yaml.
const AdminPasswordPlaceholder = "CHANGE_ME"

func newAdminSpec(h *inventory.Host, ssh inventory.SSHConfig) *spec.AdminServerSpec {
	// Emit CHANGE_ME placeholders so the generated cluster.yaml matches
	// the convention in examples/typical.yaml and, importantly, so the
	// deployed admin UI isn't left unauthenticated by omission. The
	// AdminServerSpec only writes auth flags when they're set, so
	// leaving these empty silently produces an open admin UI.
	return &spec.AdminServerSpec{
		Ip:            h.IP,
		PortSsh:       ssh.Port,
		Port:          DefaultAdminPort,
		AdminUser:     "admin",
		AdminPassword: AdminPasswordPlaceholder,
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

// deriveFolders maps probed disks onto FolderSpec entries per the
// design doc's three-bucket classification (cluster-claimed / fresh /
// foreign-or-fs-without-claim). For each kept disk, emit a /data<N>
// folder and compute Max from usable size (after reserve_pct headroom,
// capped at 10 GiB).
//
// When the host has no eligible disks the result is an empty slice;
// Generate then drops the host's volume role entirely and records the
// host in Report.VolumeHostsNoDisks. Emitting a volume_server with
// `folders: []` would silently start `weed volume` against the working
// directory because addToBuffer omits -dir for an empty list.
func deriveFolders(facts probe.HostFacts, disk inventory.DiskDefaults, volumeSizeLimitMB int) []*spec.FolderSpec {
	reservePct := disk.ReservePct
	if reservePct == 0 {
		reservePct = 5 // documented default
	}

	// Keep literal exclusions and prefix exclusions in separate
	// collections. Lumping them together would make a literal like
	// "/dev/nvme0n1" silently exclude "/dev/nvme0n10" under a naive
	// HasPrefix sweep. inventory.validateDeviceGlob has already
	// rejected anything fancier than an optional trailing '*'.
	literalExcluded := make(map[string]struct{}, len(disk.Exclude))
	var prefixExcluded []string
	for _, e := range disk.Exclude {
		if len(e) > 0 && e[len(e)-1] == '*' {
			prefixExcluded = append(prefixExcluded, e[:len(e)-1])
		} else {
			literalExcluded[e] = struct{}{}
		}
	}

	// Walk the probed disks twice. First pass: classify each disk into
	// "claimed by the cluster at /data<N>" (current mount or fstab),
	// "fresh" (no fs, no claim — eligible for provisioning), or
	// "skip" (foreign mount, filesystem with no /data claim, excluded).
	// Second pass: allocate /data<N> slots to fresh disks, skipping
	// numbers already claimed.
	type classified struct {
		disk    probe.DiskFact
		mount   string // /dataN
		slot    int    // N
		isFresh bool   // needs provisioning + slot allocation
	}
	reserved := make(map[int]struct{})
	var entries []classified

	for _, d := range facts.Disks {
		if isExcluded(d.Path, literalExcluded, prefixExcluded) {
			continue
		}
		// Effective mountpoint: kernel's view first, fall back to
		// fstab-declared. Lets us recognize a previously deployed
		// disk that just hasn't been mounted yet on this boot.
		effective := d.MountPoint
		if effective == "" {
			effective = d.FstabMountPoint
		}
		if effective != "" {
			if n, ok := parseClusterDataMount(effective); ok {
				reserved[n] = struct{}{}
				entries = append(entries, classified{disk: d, mount: effective, slot: n})
			}
			// Foreign mount (/, /home, /var/lib/docker, …) — skip
			// silently. We never reformat a disk we didn't claim.
			continue
		}
		if d.FSType != "" {
			// Has a filesystem but no mount and no fstab entry. Most
			// likely a disk the operator hand-formatted for some
			// other use. Be conservative: skip and don't reformat.
			continue
		}
		entries = append(entries, classified{disk: d, isFresh: true})
	}

	// Allocate /data<N> for fresh disks, walking N upward and skipping
	// reserved slots. Iterate the fresh disks in path order so the
	// allocation is deterministic across probe runs.
	var freshOrder []int
	for i := range entries {
		if entries[i].isFresh {
			freshOrder = append(freshOrder, i)
		}
	}
	sort.Slice(freshOrder, func(a, b int) bool {
		return entries[freshOrder[a]].disk.Path < entries[freshOrder[b]].disk.Path
	})
	nextSlot := 1
	for _, i := range freshOrder {
		for {
			if _, taken := reserved[nextSlot]; !taken {
				entries[i].slot = nextSlot
				entries[i].mount = fmt.Sprintf("/data%d", nextSlot)
				reserved[nextSlot] = struct{}{}
				nextSlot++
				break
			}
			nextSlot++
		}
	}

	// Sort the final folder list by slot so /data1 always comes before
	// /data2, regardless of whether each was claimed or fresh.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].slot < entries[j].slot
	})

	folders := make([]*spec.FolderSpec, 0, len(entries))
	for _, e := range entries {
		folders = append(folders, &spec.FolderSpec{
			Folder:   e.mount,
			DiskType: diskTypeFor(e.disk, disk),
			Max:      computeMax(e.disk.Size, reservePct, volumeSizeLimitMB),
		})
	}
	return folders
}

// clusterDataMountRE recognizes the /data<N> mountpoint convention used
// by prepareUnmountedDisks and the planner's allocation. Anything else
// (e.g. /, /var/lib/docker, /home) is a foreign mount we don't manage.
var clusterDataMountRE = regexp.MustCompile(`^/data(\d+)$`)

// parseClusterDataMount returns (N, true) when mp is "/data<N>" and
// (0, false) for anything else. Used by deriveFolders to recognize
// disks that are already part of (or reserved for) this cluster.
func parseClusterDataMount(mp string) (int, bool) {
	m := clusterDataMountRE.FindStringSubmatch(mp)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

func isExcluded(path string, literals map[string]struct{}, prefixes []string) bool {
	if _, ok := literals[path]; ok {
		return true
	}
	for _, p := range prefixes {
		if p != "" && strings.HasPrefix(path, p) {
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

