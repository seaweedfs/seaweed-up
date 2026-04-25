package manager

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	disksLib "github.com/seaweedfs/seaweed-up/pkg/disks"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
	"github.com/seaweedfs/seaweed-up/scripts"
)

func (m *Manager) DeployVolumeServer(masters []string, volumeServerSpec *spec.VolumeServerSpec, index int) error {
	target := fmt.Sprintf("%s:%d", volumeServerSpec.Ip, volumeServerSpec.PortSsh)

	// When the spec is plan-generated (PlannedDisksBySSHTarget non-nil),
	// every mountpoint the volume server points at MUST have a
	// plan-approved disk to back it. The check uses the per-target
	// aggregate (sum across every volume_server on this SSH target)
	// when DeployCluster pre-computed it, falling back to this spec's
	// own folder count otherwise. The aggregate matters for
	// --volume-server-shape=per-disk: N one-folder specs on the same
	// host need N approved disks, not just 1 each.
	//
	// Without this guard:
	//   - A target absent from the allowlist would empty the
	//     candidate disks; ensureVolumeFolders would mkdir on rootfs.
	//   - A target with FEWER approved disks than folders would mount
	//     some folders on real disks and silently leave the rest on
	//     rootfs. `weed volume` would then start with -dir pointing
	//     at a mix of real disks and root-filesystem directories.
	if m.PlannedDisksBySSHTarget != nil {
		needed, perTarget := requiredMountsForTarget(m, volumeServerSpec, target)
		if needed > 0 {
			approved := m.PlannedDisksBySSHTarget[target]
			if len(approved) < needed {
				scope := "this volume_server"
				if perTarget {
					n := m.volumeServerCountByTarget[target]
					if n <= 0 {
						n = 1
					}
					scope = fmt.Sprintf("the %d volume_server(s) on this target", n)
				}
				return fmt.Errorf(
					"%s expects %d mountpoint(s) in cluster.yaml but %s has only %d plan-approved disk(s); "+
						"refusing to start volume on root filesystem — re-run "+
						"`cluster plan -o <spec>.yaml --overwrite` (likely a stale .deploy-disks.json sidecar)",
					scope, needed, target, len(approved))
			}
		}
	}

	return operator.ExecuteRemote(fmt.Sprintf("%s:%d", volumeServerSpec.Ip, volumeServerSpec.PortSsh), m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {

		component := "volume"
		componentInstance := fmt.Sprintf("%s%d", component, index)
		var buf bytes.Buffer
		volumeServerSpec.WriteToBuffer(masters, &buf)

		if m.PrepareVolumeDisks {
			if err := m.prepareUnmountedDisksOnce(op, target); err != nil {
				return fmt.Errorf("prepare disks: %v", err)
			}
		}

		// Plan-generated specs get a runtime mountpoint check before
		// we mkdir or start anything. The static allowlist guard above
		// only verifies the SIDECAR has enough approved paths; it
		// can't catch the case where prepareUnmountedDisks dropped an
		// approved device because it acquired a partition or an
		// unrelated mount between plan and deploy. Without this check,
		// ensureVolumeFolders would mkdir the missing /data<N> on
		// rootfs and weed volume would write data into the OS root.
		//
		// Gated on PlanGenerated, NOT PlannedDisksBySSHTarget: a
		// plan-generated cluster.yaml deployed with --mount-disks=false
		// keeps the sidecar optional and reaches DeployVolumeServer
		// with PlannedDisksBySSHTarget==nil, but the spec still
		// promises every -dir is a mountpoint. Without this gate the
		// no-sidecar path would silently start weed volume on rootfs.
		if m.PlanGenerated {
			if err := m.verifyVolumeFoldersAreMountpoints(op, volumeServerSpec); err != nil {
				return err
			}
		}

		if err := m.ensureVolumeFolders(op, volumeServerSpec); err != nil {
			return err
		}

		return m.deployComponentInstance(op, component, componentInstance, &buf)

	})
}

// volumeServerFolderPaths returns every -dir / -dir.idx path the spec
// will pass to weed volume. Empty/nil entries are skipped.
func volumeServerFolderPaths(vs *spec.VolumeServerSpec) []string {
	paths := make([]string, 0, len(vs.Folders)+1)
	for _, f := range vs.Folders {
		if f != nil && f.Folder != "" {
			paths = append(paths, f.Folder)
		}
	}
	if vs.IdxFolder != "" {
		paths = append(paths, vs.IdxFolder)
	}
	return paths
}

// verifyVolumeFoldersAreMountpoints confirms each -dir / -dir.idx path
// is a real mountpoint on the host, not a regular directory on rootfs.
// Bridges the gap between the static allowlist count (pre-flight) and
// the actual outcome of prepareUnmountedDisks: even with enough
// approved paths in the sidecar, prepareUnmountedDisks can produce
// fewer mounts if one of the devices acquired a partition, was
// mounted elsewhere, or disappeared between plan and deploy. Without
// this runtime check, ensureVolumeFolders would mkdir the missing
// path on rootfs and weed volume would silently write data into the
// OS root filesystem. Uses `mountpoint` from util-linux (universal on
// real Linux hosts; busybox provides it on Alpine/embedded). One
// round-trip: the script echoes any path that fails the check, so
// the error names every drift in one go.
func (m *Manager) verifyVolumeFoldersAreMountpoints(op operator.CommandOperator, vs *spec.VolumeServerSpec) error {
	paths := volumeServerFolderPaths(vs)
	if len(paths) == 0 {
		return nil
	}
	var b strings.Builder
	for i, p := range paths {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(shellSingleQuote(p))
	}
	cmd := fmt.Sprintf(`for p in %s; do mountpoint -q "$p" || echo "$p"; done`, b.String())
	out, err := op.Output(cmd)
	if err != nil {
		return fmt.Errorf("verify volume folder mountpoints: %v", err)
	}
	var missing []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			missing = append(missing, line)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf(
			"plan-approved volume folder(s) are not mountpoints after prepare on this host: %v — "+
				"starting weed volume now would write data into the root filesystem. "+
				"Likely cause: a device drifted between plan and deploy (acquired a partition, "+
				"was mounted elsewhere, or disappeared). Re-run "+
				"`cluster plan -o <spec>.yaml --overwrite` to refresh the sidecar, then re-deploy.",
			missing)
	}
	return nil
}

// requiredMountsForTarget returns the number of mountpoints the
// allowlist for target must cover. Prefers the aggregate count
// pre-computed by DeployCluster (correct for per-disk shape across
// multiple volume_servers on the same host); falls back to this
// spec's own folder count when the manager wasn't initialized via
// DeployCluster (e.g. in unit tests that exercise DeployVolumeServer
// directly). The bool reports whether the aggregate was used so the
// error message can frame the count correctly.
func requiredMountsForTarget(m *Manager, vs *spec.VolumeServerSpec, target string) (int, bool) {
	if n, ok := m.requiredDisksByTarget[target]; ok {
		return n, true
	}
	n := len(vs.Folders)
	if vs.IdxFolder != "" {
		n++
	}
	return n, false
}


// prepareUnmountedDisksOnce gates prepareUnmountedDisks behind a
// per-SSH-target sync.Once. With the per-disk volume_server shape
// (multiple volume_servers on the same IP), DeployCluster fans the
// deploys out concurrently and each would independently call
// prepareUnmountedDisks, racing on mkfs/mount assignment. Routing
// through a once-per-target gate makes disk prep effectively a
// per-host pre-step while keeping the existing DeployVolumeServer
// signature. target is `<ip>:<ssh-port>` so inventories where two
// SSH endpoints share an IP each get their own gate.
func (m *Manager) prepareUnmountedDisksOnce(op operator.CommandOperator, target string) error {
	v, _ := m.prepareDisksOnce.LoadOrStore(target, &prepareDisksGate{})
	gate := v.(*prepareDisksGate)
	gate.once.Do(func() {
		gate.err = m.prepareUnmountedDisks(op, target)
	})
	return gate.err
}


// ensureVolumeFolders creates each configured -dir path on the remote host
// before the volume server starts. SeaweedFS volume refuses to boot if any of
// the -dir paths does not exist (Fatalf "Check Data Folder(-dir) Writable"),
// so this must run on both initial deploys and rolling upgrades. The
// IdxFolder (-dir.idx target) gets the same treatment when set.
func (m *Manager) ensureVolumeFolders(op operator.CommandOperator, volumeServerSpec *spec.VolumeServerSpec) error {
	for _, folder := range volumeServerSpec.Folders {
		if folder == nil || folder.Folder == "" {
			continue
		}
		if err := m.sudo(op, fmt.Sprintf("mkdir -p %s", folder.Folder)); err != nil {
			return fmt.Errorf("create volume data folder %s: %v", folder.Folder, err)
		}
	}
	if volumeServerSpec.IdxFolder != "" {
		if err := m.sudo(op, fmt.Sprintf("mkdir -p %s", volumeServerSpec.IdxFolder)); err != nil {
			return fmt.Errorf("create volume idx folder %s: %v", volumeServerSpec.IdxFolder, err)
		}
	}
	return nil
}

func (m *Manager) ResetVolumeServer(volumeServerSpec *spec.VolumeServerSpec, index int) error {
	return operator.ExecuteRemote(fmt.Sprintf("%s:%d", volumeServerSpec.Ip, volumeServerSpec.PortSsh), m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {

		component := "volume"
		componentInstance := fmt.Sprintf("%s%d", component, index)

		return m.sudo(op, fmt.Sprintf("rm -Rf %s/%s/*", m.dataDir, componentInstance))
	})
}

func (m *Manager) StartVolumeServer(volumeServerSpec *spec.VolumeServerSpec, index int) error {
	return operator.ExecuteRemote(fmt.Sprintf("%s:%d", volumeServerSpec.Ip, volumeServerSpec.PortSsh), m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {

		component := "volume"
		componentInstance := fmt.Sprintf("%s%d", component, index)

		return m.sudo(op, fmt.Sprintf("systemctl start seaweed_%s.service", componentInstance))
	})
}

func (m *Manager) StopVolumeServer(volumeServerSpec *spec.VolumeServerSpec, index int) error {
	return operator.ExecuteRemote(fmt.Sprintf("%s:%d", volumeServerSpec.Ip, volumeServerSpec.PortSsh), m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {

		component := "volume"
		componentInstance := fmt.Sprintf("%s%d", component, index)

		return m.sudo(op, fmt.Sprintf("systemctl stop seaweed_%s.service", componentInstance))
	})
}

func (m *Manager) prepareUnmountedDisks(op operator.CommandOperator, target string) error {
	println("prepareUnmountedDisks...")

	// Realize any pending fstab entries before we look at the kernel's
	// mount table. Without this, a previously-deployed disk that
	// hasn't auto-mounted yet (boot race, manual umount, fstab edited
	// but no `mount -a`) would appear as MountPoint="" with FSType
	// set; our existing skip-if-fstype-set rule then leaves it out of
	// the disks map. The downstream allocation loop would then pick a
	// fresh /data<N> and try to mount with a different mountpoint than
	// the fstab entry, which fails (or worse, succeeds at a stale
	// path). Idempotent: a no-op when everything is already mounted.
	// nofail entries (or broken ones) are tolerated via `|| true`.
	_ = m.sudo(op, "mount -a 2>/dev/null || true")

	// Use the shared default prefix set so probe and prepare always
	// scan the same device families (see disksLib.DefaultDevicePrefixes
	// for the per-prefix rationale). Harmless on systems where a prefix
	// isn't present — no devices match.
	devices, mountpoints, err := disksLib.ListBlockDevices(op, disksLib.DefaultDevicePrefixes)
	if err != nil {
		return fmt.Errorf("list device: %v", err)
	}
	fmt.Printf("mountpoints: %+v\n", mountpoints)

	disks := make(map[string]*disksLib.BlockDevice)

	// find all disks
	for _, dev := range devices {
		if dev.Type == "disk" {
			disks[dev.Path] = dev
		}
	}

	fmt.Printf("disks0: %+v\n", disks)

	// remove disks that have any partitions. Use the shared
	// disksLib.IsPartitionOf so we encode the kernel's actual naming
	// rule (digit-ending parents use 'p' separator) instead of a
	// naive HasPrefix that would wrongly drop /dev/nvme0n1 just
	// because /dev/nvme0n10p1 exists on a multi-namespace host.
	for _, dev := range devices {
		if dev.Type != "part" {
			continue
		}
		for parentPath := range disks {
			if disksLib.IsPartitionOf(dev.Path, parentPath) {
				delete(disks, parentPath)
			}
		}
	}
	fmt.Printf("disks1: %+v\n", disks)

	// Honor the plan-side allowlist when set. PlannedDisksBySSHTarget
	// is populated from the deploy-disks.json sidecar `cluster plan`
	// writes alongside cluster.yaml; it carries the per-target set of
	// /dev paths plan classified as eligible (fresh + non-ephemeral
	// + not foreign-mounted + not excluded by inventory). When the
	// map is non-nil it is authoritative — a target that doesn't
	// appear in it gets an empty allow set, NOT a free-for-all
	// fallback. Otherwise a plan that classified zero disks for a
	// host would silently get all of that host's unmounted disks
	// formatted, defeating the planner's exclude/ephemeral rules.
	if m.PlannedDisksBySSHTarget != nil {
		allow := m.PlannedDisksBySSHTarget[target]
		for k := range disks {
			if _, kept := allow[k]; !kept {
				delete(disks, k)
			}
		}
	}

	// remove already has mount point
	for k, dev := range disks {
		if dev.MountPoint != "" {
			delete(disks, k)
		}
	}
	fmt.Printf("disks2: %+v\n", disks)

	// Iterate in sorted-path order so /data<N> assignment is
	// deterministic and matches the planner's ordering in
	// pkg/cluster/plan.deriveFolders. Without this, deploy's Go-map
	// iteration could mount disk B at /data1 while cluster.yaml's
	// FolderSpec /data1 has Max/DiskType derived from disk A's
	// metrics — the volume server would then run with flags that
	// don't fit the actual disk it's writing to.
	orderedPaths := make([]string, 0, len(disks))
	for k := range disks {
		orderedPaths = append(orderedPaths, k)
	}
	sort.Strings(orderedPaths)

	// format disk if no fstype, then resolve the resulting UUID so the fstab
	// entry written below can mount by UUID instead of by device path.
	for _, k := range orderedPaths {
		dev := disks[k]
		if dev.FilesystemType == "" {
			info("mkfs " + dev.Path)
			if err := m.sudo(op, fmt.Sprintf("mkfs.ext4 %s", dev.Path)); err != nil {
				return fmt.Errorf("create file system on %s: %v", dev.Path, err)
			}
		}
		if dev.UUID == "" {
			uuid, err := m.probeDiskUUID(op, dev.Path)
			if err != nil {
				return fmt.Errorf("resolve UUID for %s: %v", dev.Path, err)
			}
			dev.UUID = uuid
		}
	}

	// mount them
	for _, k := range orderedPaths {
		dev := disks[k]
		if dev.MountPoint == "" {
			var targetMountPoint = ""
			for i := 1; i < 100; i++ {
				t := fmt.Sprintf("/data%d", i)
				if _, found := mountpoints[t]; found {
					continue
				}
				targetMountPoint = t
				mountpoints[t] = struct{}{}
				break
			}
			if targetMountPoint == "" {
				return fmt.Errorf("no good mount point")
			}

			data := map[string]interface{}{
				"DevicePath": dev.Path,
				"DeviceUUID": dev.UUID,
				"MountPoint": targetMountPoint,
			}
			prepareScript, err := scripts.RenderScript("prepare_disk.sh", data)
			if err != nil {
				return err
			}
			info("Installing mount_" + dev.DeviceName + ".sh")
			err = op.Upload(prepareScript, fmt.Sprintf("/tmp/mount_%s.sh", dev.DeviceName), "0755")
			if err != nil {
				return fmt.Errorf("error received during upload mount script: %s", err)
			}

			info(fmt.Sprintf("mount %s (UUID=%s) at %s", dev.DeviceName, dev.UUID, targetMountPoint))
			err = op.Execute(fmt.Sprintf("cat /tmp/mount_%s.sh | SUDO_PASS=%s sh -\n", dev.DeviceName, shellSingleQuote(m.sudoPass)))
			if err != nil {
				return fmt.Errorf("error received during mount: %s", err)
			}

		}
	}

	return nil
}

// probeDiskUUID reads the filesystem UUID of path via blkid. After mkfs the
// superblock is written but udev may not yet have re-read it, so we let it
// settle and retry a few times before giving up. blkid needs root to read
// raw block devices on most distros (and -p always needs root), so we wrap
// it the same way m.sudo does. Returning an error here aborts the deploy
// rather than writing a broken fstab entry that would leave the host unable
// to boot.
func (m *Manager) probeDiskUUID(op operator.CommandOperator, path string) (string, error) {
	// Best-effort settle. Ignore errors — `udevadm` is missing on some
	// minimal images and that's OK; the retry loop below will still pick
	// up the UUID once it's available.
	_ = m.sudo(op, "command -v udevadm >/dev/null 2>&1 && udevadm settle || true")

	// -p bypasses the blkid cache and re-probes the superblock directly,
	// which is what we want right after mkfs.
	probeCmd := fmt.Sprintf("blkid -p -s UUID -o value %s", shellSingleQuote(path))
	if m.sudoPass != "" {
		probeCmd = fmt.Sprintf("echo %s | sudo -S %s", shellSingleQuote(m.sudoPass), probeCmd)
	}

	const attempts = 5
	var lastErr error
	for i := 0; i < attempts; i++ {
		out, err := op.Output(probeCmd)
		if err == nil {
			uuid := strings.TrimSpace(string(out))
			if uuid != "" {
				return uuid, nil
			}
		} else {
			lastErr = err
		}
		time.Sleep(500 * time.Millisecond)
	}
	if lastErr != nil {
		return "", fmt.Errorf("blkid returned no UUID after %d attempts: %v", attempts, lastErr)
	}
	return "", fmt.Errorf("blkid returned no UUID after %d attempts", attempts)
}
