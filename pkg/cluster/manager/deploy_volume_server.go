package manager

import (
	"bytes"
	"fmt"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/disks"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
	"github.com/seaweedfs/seaweed-up/scripts"
	"math"
	"strings"
	"time"
)

func (m *Manager) UpdateDynamicVolumes(ip string, folders []spec.FolderSpec) {
	m.DynamicConfig.Lock()
	m.DynamicConfig.Changed = true
	m.DynamicConfig.DynamicVolumeServers[ip] = folders
	m.DynamicConfig.Unlock()
}

func (m *Manager) GetDynamicVolumes(ip string) []spec.FolderSpec {
	m.DynamicConfig.Lock()
	defer m.DynamicConfig.Unlock()

	if m, ok := m.DynamicConfig.DynamicVolumeServers[ip]; ok {
		return m
	}
	return []spec.FolderSpec{}
}

func (m *Manager) DeployVolumeServer(masters []string, globalOptions spec.GlobalOptions, volumeServerSpec *spec.VolumeServerSpec, index int) error {
	return operator.ExecuteRemote(fmt.Sprintf("%s:%d", volumeServerSpec.Ip, volumeServerSpec.PortSsh), m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {

		component := "volume"
		componentInstance := fmt.Sprintf("%s%d", component, index)

		// Prepare dynamic folders
		dynamicFolders := m.GetDynamicVolumes(volumeServerSpec.Ip)
		if m.PrepareVolumeDisks {
			err, changed := m.prepareUnmountedDisks(op, &dynamicFolders, globalOptions.VolumeSizeLimitMB)
			if err != nil {
				return fmt.Errorf("prepare disks: %v", err)
			}
			if changed {
				// Pass change info into upper layer
				m.UpdateDynamicVolumes(volumeServerSpec.Ip, dynamicFolders)
			}
		}

		// Update server specification for current server
		for _, fld := range dynamicFolders {
			flx := fld
			volumeServerSpec.Folders = append(volumeServerSpec.Folders, &flx)
		}

		var buf bytes.Buffer
		volumeServerSpec.WriteToBuffer(masters, &buf)

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

func (m *Manager) prepareUnmountedDisks(op operator.CommandOperator, dynamicFolders *[]spec.FolderSpec, volumeSizeLimitMB int) (err error, changed bool) {
	println("prepareUnmountedDisks...")
	devices, mountpoints, err := disks.ListBlockDevices(op, []string{"/dev/sd", "/dev/nvme"})
	if err != nil {
		return fmt.Errorf("list device: %v", err), false
	}
	fmt.Printf("mountpoints: %+v\n", mountpoints)

	diskList := make(map[string]*disks.BlockDevice)

	// find all diskList
	for _, dev := range devices {
		if dev.Type == "disk" {
			diskList[dev.Path] = dev
		}
	}

	fmt.Printf("All devices: %+v\n", diskList)

	// remove diskList already has partitions
	for _, dev := range devices {
		if dev.Type == "part" {
			for parentPath, _ := range diskList {
				if strings.HasPrefix(dev.Path, parentPath) {
					// the disk is already partitioned
					delete(diskList, parentPath)
				}
			}
		}
	}
	fmt.Printf("Devices without partition: %+v\n", diskList)

	// remove already has mount point
	for k, dev := range diskList {
		if dev.MountPoint != "" {
			delete(diskList, k)
		} else if dev.FilesystemType != "" {
			delete(diskList, k)
		}
	}
	fmt.Printf("Devices without mountpoint and filesystem: %+v\n", diskList)

	// Process all unused RAW devices
	for _, dev := range diskList {
		// format disk
		info("mkfs " + dev.Path)
		if err := m.sudo(op, fmt.Sprintf("mkfs.ext4 %s", dev.Path)); err != nil {
			return fmt.Errorf("create file system on %s: %v", dev.Path, err), changed
		}

		// Wait 2 sec for kernel data sync
		time.Sleep(2 * time.Second)

		// Get UUID
		uuid, err := disks.GetDiskUUID(op, dev.Path)
		if err != nil {
			return fmt.Errorf("get disk UUID on %s: %v", dev.Path, err), changed
		}

		if uuid == "" {
			return fmt.Errorf("get empty disk UUID for %s", dev.Path), changed
		}

		diskList[dev.Path].UUID = uuid
		dev.UUID = uuid

		fmt.Printf("* disk [%s] UUID: [%s]", dev.Path, dev.UUID)

		// Allocate spare mountpoint
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
			return fmt.Errorf("no good mount point"), changed
		}

		data := map[string]interface{}{
			"DevicePath": dev.Path,
			"DeviceUUID": dev.UUID,
			"MountPoint": targetMountPoint,
		}
		prepareScript, err := scripts.RenderScript("prepare_disk.sh", data)
		if err != nil {
			return err, changed
		}
		info("Installing mount_" + dev.DeviceName + ".sh")
		err = op.Upload(prepareScript, fmt.Sprintf("/tmp/mount_%s.sh", dev.DeviceName), "0755")
		if err != nil {
			return fmt.Errorf("error received during upload mount script: %s", err), changed
		}

		info("mount " + dev.DeviceName + " with UUID " + dev.UUID + " into " + targetMountPoint + "...")
		err = op.Execute(fmt.Sprintf("cat /tmp/mount_%s.sh | SUDO_PASS=\"%s\" sh -\n", dev.DeviceName, m.sudoPass))
		if err != nil {
			return fmt.Errorf("error received during mount %s with UUID %s: %s", dev.DeviceName, dev.UUID, err), changed
		}

		// Max calculation: reserve min(5%, 10Gb)
		usableSizeMb := dev.Size / 1024 / 1024
		if usableSizeMb > 200*1024 {
			usableSizeMb -= 10 * 1024
		} else {
			usableSizeMb = uint64(int(math.Floor(float64(usableSizeMb) * 0.95)))
		}

		// New volume is mounted, provision it into dynamicFolders
		folderSpec := spec.FolderSpec{
			Folder:      targetMountPoint,
			DiskType:    "hdd",
			BlockDevice: dev.Path,
			UUID:        dev.UUID,
			Max:         int(math.Floor(float64(usableSizeMb) / float64(volumeSizeLimitMB))),
		}

		*dynamicFolders = append(*dynamicFolders, folderSpec)
		changed = true
	}

	return nil, changed
}
