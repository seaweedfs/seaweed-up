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
	// every emitted volume_server must have at least one allowlisted
	// disk for its SSH target. Without this guard, a target absent from
	// the allowlist would empty the candidate disks at
	// prepareUnmountedDisks time, ensureVolumeFolders would mkdir the
	// folders on the OS rootfs, and `weed volume` would happily start
	// writing data there. Refusing here preserves the planner's
	// "disks-only-on-disks" guarantee and points the operator at the
	// inconsistency (likely an inventory edit without re-running plan,
	// or a stale .deploy-disks.json).
	if m.PlannedDisksBySSHTarget != nil && len(volumeServerSpec.Folders) > 0 {
		if len(m.PlannedDisksBySSHTarget[target]) == 0 {
			return fmt.Errorf(
				"volume_server %s has %d folder(s) in cluster.yaml but %s has no plan-approved disks; "+
					"refusing to start volume on root filesystem — re-run `cluster plan -o <spec>.yaml --overwrite` "+
					"or hand-edit cluster.yaml + delete the .deploy-disks.json sidecar",
				target, len(volumeServerSpec.Folders), target)
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

		if err := m.ensureVolumeFolders(op, volumeServerSpec); err != nil {
			return err
		}

		return m.deployComponentInstance(op, component, componentInstance, &buf)

	})
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
// so this must run on both initial deploys and rolling upgrades.
func (m *Manager) ensureVolumeFolders(op operator.CommandOperator, volumeServerSpec *spec.VolumeServerSpec) error {
	for _, folder := range volumeServerSpec.Folders {
		if folder == nil || folder.Folder == "" {
			continue
		}
		if err := m.sudo(op, fmt.Sprintf("mkdir -p %s", folder.Folder)); err != nil {
			return fmt.Errorf("create volume data folder %s: %v", folder.Folder, err)
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

	// Default disk prefix set, kept in sync with the planner's
	// pkg/cluster/probe.defaultDevicePrefixes:
	//   /dev/sd    SCSI / SATA, Azure managed disks, GCP SCSI PDs
	//   /dev/nvme  NVMe SSDs, AWS Nitro EBS, GCP NVMe PDs
	//   /dev/xvd   Xen — older AWS, XenServer/XCP-ng
	//   /dev/vd    KVM virtio — Vultr, Linode, Hetzner, OpenStack
	// Harmless on systems where the prefix isn't present.
	devices, mountpoints, err := disksLib.ListBlockDevices(op, []string{"/dev/sd", "/dev/nvme", "/dev/xvd", "/dev/vd"})
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
