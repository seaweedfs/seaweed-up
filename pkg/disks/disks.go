package disks

import (
	"bufio"
	"bytes"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
	"regexp"
	"strconv"
	"strings"
)

// DefaultDevicePrefixes lists the /dev path prefixes the planner and
// the deploy-time disk preparer treat as candidate block devices when
// the operator hasn't supplied an explicit globs list:
//
//	/dev/sd    SCSI / SATA, Azure managed disks, GCP SCSI PDs
//	/dev/nvme  NVMe SSDs, AWS Nitro EBS, GCP NVMe PDs
//	/dev/xvd   Xen — older AWS, XenServer/XCP-ng
//	/dev/vd    KVM virtio — Vultr, Linode, Hetzner, OpenStack
//
// Single source of truth so probe and prepareUnmountedDisks always
// scan the same device families. Adding a prefix here exposes it to
// both sides at once.
var DefaultDevicePrefixes = []string{"/dev/sd", "/dev/nvme", "/dev/xvd", "/dev/vd"}

// IsPartitionOf returns true when partPath is a kernel-level partition
// of parentPath. Linux uses two conventions, distinguished by whether
// the parent name ends in a digit:
//
//   - parent ends in a letter (sda, vdb, xvdc) → partition is parent +
//     digits: sda1, sda12.
//   - parent ends in a digit  (nvme0n1, loop0, mmcblk0) → partition is
//     parent + 'p' + digits: nvme0n1p1, loop0p3.
//
// Naive HasPrefix is unsafe both ways: it would treat /dev/nvme0n10 as
// a partition of /dev/nvme0n1 on multi-namespace hosts, and treat
// /dev/sda12 as a partition of /dev/sda1 (which is itself a partition,
// not a parent). Encoding the kernel's separator rule here keeps the
// match exact without parsing lsblk's PKNAME column.
//
// Shared by pkg/cluster/probe (pre-deploy classification) and
// pkg/cluster/manager (deploy-time prepareUnmountedDisks) so both
// sides agree on which disks are top-level vs. partition children.
func IsPartitionOf(partPath, parentPath string) bool {
	if parentPath == "" || len(partPath) <= len(parentPath) {
		return false
	}
	if partPath[:len(parentPath)] != parentPath {
		return false
	}
	suffix := partPath[len(parentPath):]
	parentEndsInDigit := parentPath[len(parentPath)-1] >= '0' && parentPath[len(parentPath)-1] <= '9'
	if parentEndsInDigit {
		if suffix[0] != 'p' {
			return false
		}
		suffix = suffix[1:]
	}
	if suffix == "" {
		return false
	}
	for _, c := range suffix {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

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
	// Rotational is true for spinning disks, false for SSDs/NVMe, and nil
	// when lsblk's ROTA column is empty (virtio, loop, some
	// device-mapper nodes). Tri-state so the planner can distinguish
	// "known SSD" from "we couldn't tell" — treating unknown as SSD by
	// default would silently mis-tag HDDs on quirky hardware. Parsed
	// from lsblk's ROTA column (1 = rotational, 0 = not, empty =
	// unknown).
	Rotational *bool
	// Model is the drive model string reported by lsblk's MODEL column
	// (e.g. "Samsung SSD 870 EVO"). Purely informational — surfaced in
	// probe output for audit/debug; not used for any decision.
	Model string
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
				"ROTA",  // 1 for rotational, 0 for non-rotational
				"MODEL", // device model string
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
						println("valid path", dev.Path)
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
			case "ROTA":
				switch value {
				case "1":
					t := true
					dev.Rotational = &t
				case "0":
					f := false
					dev.Rotational = &f
				}
				// Empty or any other value leaves Rotational as nil
				// ("unknown"). See the field comment above.
			case "MODEL":
				dev.Model = strings.TrimSpace(value)
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
