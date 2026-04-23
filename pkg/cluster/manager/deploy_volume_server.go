package manager

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/disks"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
	"github.com/seaweedfs/seaweed-up/scripts"
)

func (m *Manager) DeployVolumeServer(masters []string, volumeServerSpec *spec.VolumeServerSpec, index int) error {
	return operator.ExecuteRemote(fmt.Sprintf("%s:%d", volumeServerSpec.Ip, volumeServerSpec.PortSsh), m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {

		component := "volume"
		componentInstance := fmt.Sprintf("%s%d", component, index)
		var buf bytes.Buffer
		volumeServerSpec.WriteToBuffer(masters, &buf)

		if m.PrepareVolumeDisks {
			if err := m.prepareUnmountedDisks(op); err != nil {
				return fmt.Errorf("prepare disks: %v", err)
			}
		}

		if err := m.ensureVolumeFolders(op, volumeServerSpec); err != nil {
			return err
		}

		return m.deployComponentInstance(op, component, componentInstance, &buf)

	})
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

func (m *Manager) prepareUnmountedDisks(op operator.CommandOperator) error {
	println("prepareUnmountedDisks...")
	devices, mountpoints, err := disks.ListBlockDevices(op, []string{"/dev/sd", "/dev/nvme"})
	if err != nil {
		return fmt.Errorf("list device: %v", err)
	}
	fmt.Printf("mountpoints: %+v\n", mountpoints)

	disks := make(map[string]*disks.BlockDevice)

	// find all disks
	for _, dev := range devices {
		if dev.Type == "disk" {
			disks[dev.Path] = dev
		}
	}

	fmt.Printf("disks0: %+v\n", disks)

	// remove disks already has partitions
	for _, dev := range devices {
		if dev.Type == "part" {
			for parentPath := range disks {
				if strings.HasPrefix(dev.Path, parentPath) {
					// the disk is already partitioned
					delete(disks, parentPath)
				}
			}
		}
	}
	fmt.Printf("disks1: %+v\n", disks)

	// remove already has mount point
	for k, dev := range disks {
		if dev.MountPoint != "" {
			delete(disks, k)
		}
	}
	fmt.Printf("disks2: %+v\n", disks)

	// format disk if no fstype, then resolve the resulting UUID so the fstab
	// entry written below can mount by UUID instead of by device path.
	for _, dev := range disks {
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
	for _, dev := range disks {
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
