package disks

import (
	"bufio"
	"bytes"
	"fmt"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
	"regexp"
	"strconv"
	"strings"
)

type BlockDevice struct {
	DeviceName     string
	Path           string
	Size           uint64
	Label          string
	UUID           string
	FilesystemType string
	InUse          bool
	MountPoint     string
	SerialId       string
	Type           string
}

func GetDiskUUID(op operator.CommandOperator, devName string) (UUID string, err error) {
	devices, _, err := ListBlockDevices(op, []string{devName})

	if err != nil {
		return "", err
	}
	for _, dev := range devices {
		if dev.Path == devName {
			return dev.UUID, nil
		}
	}
	return "", fmt.Errorf("uuid not found")
}

func ListBlockDevices(op operator.CommandOperator, prefixes []string) (output []*BlockDevice, mountpoints map[string]struct{}, err error) {
	mountpoints = make(map[string]struct{})
	out, err := op.Output(
		strings.Join([]string{
			"lsblk",
			"-b", // output size in bytes
			"-P", // output fields as key=value pairs
			"-o", strings.Join([]string{
				"KNAME",      // kernel name
				"PATH",       // path
				"SIZE",       // size
				"LABEL",      // filesystem label
				"UUID",       // filesystem UUID
				"FSTYPE",     // filesystem type
				"TYPE",       // device type
				"MOUNTPOINT", // mount point
				"MAJ:MIN",    // major/minor device numbers
				"FSUSED",
			}, ","),
		}, " "))
	if err != nil {
		return
	}
	scanner := bufio.NewScanner(bytes.NewReader(out))
	nvPairPattern := regexp.MustCompile(`([A-Z:]+)=(?:"(.*?)")`)
	for scanner.Scan() {
		pairs := nvPairPattern.FindAllStringSubmatch(scanner.Text(), -1)
		dev := &BlockDevice{}
		var majorMinor string
		var hasValidPrefix bool
		for _, pair := range pairs {
			if len(pair) != 3 {
				continue
			}
			name, value := pair[1], pair[2]
			switch name {
			case "KNAME":
				dev.DeviceName = value
			case "PATH":
				dev.Path = value
				for _, prefix := range prefixes {
					if strings.HasPrefix(dev.Path, prefix) {
						hasValidPrefix = true
						//println("valid path", dev.Path)
						break
					}
				}
			case "SIZE":
				var size uint64
				size, err = strconv.ParseUint(value, 10, 64)
				if err != nil {
					return
				} else {
					dev.Size = size
				}
			case "LABEL":
				dev.Label = value
			case "UUID":
				dev.UUID = value
			case "FSTYPE":
				dev.FilesystemType = value
			case "TYPE":
				dev.Type = value
			case "MOUNTPOINT":
				dev.MountPoint = value
				mountpoints[value] = struct{}{}
			case "MAJ:MIN":
				majorMinor = pair[2]
			}
		}
		if !hasValidPrefix {
			continue
		}
		if dev.Type == "disk" {
			// Floppy disks, which have major device number 2
			if strings.HasPrefix(majorMinor, "2:") {
				continue
			}
		}
		output = append(output, dev)
	}
	return
}
