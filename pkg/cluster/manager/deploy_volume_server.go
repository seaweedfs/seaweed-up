package manager

import (
	"bytes"
	"fmt"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/disks"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
	"github.com/seaweedfs/seaweed-up/scripts"
	"strings"
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

		return m.deployComponentInstance(op, component, componentInstance, &buf)

	})
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
			for parentPath, _ := range disks {
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

	// format disk if no fstype
	for _, dev := range disks {
		if dev.FilesystemType == "" {
			info("mkfs " + dev.Path)
			if err := m.sudo(op, fmt.Sprintf("mkfs.ext4 %s", dev.Path)); err != nil {
				return fmt.Errorf("create file system on %s: %v", dev.Path, err)
			}
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

			info("mount " + dev.DeviceName + "...")
			err = op.Execute(fmt.Sprintf("cat /tmp/mount_%s.sh | SUDO_PASS=\"%s\" sh -\n", dev.DeviceName, m.sudoPass))
			if err != nil {
				return fmt.Errorf("error received during mount: %s", err)
			}

		}
	}

	return nil
}
