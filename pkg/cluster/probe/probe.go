package probe

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/inventory"
	"github.com/seaweedfs/seaweed-up/pkg/disks"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
)

// Runner is the subset of operator.CommandOperator that probe functions
// need. Declared here so tests can supply a scripted fake without having
// to stand up a real SSH session.
type Runner interface {
	Output(command string) ([]byte, error)
}

// Probe opens an SSH session to h and runs every probe function,
// collecting facts into a HostFacts. Sub-probe failures are swallowed
// into empty fields; a connection-level failure is reported via
// HostFacts.ProbeError.
func Probe(inv *inventory.Inventory, h *inventory.Host) HostFacts {
	facts := HostFacts{
		IP:       h.IP,
		ProbedAt: time.Now().UTC(),
	}

	ssh := inv.EffectiveSSH(h)
	target := fmt.Sprintf("%s:%d", h.IP, ssh.Port)
	err := operator.ExecuteRemote(target, ssh.User, ssh.Identity, "", func(op operator.CommandOperator) error {
		probeInto(op, inv, h, &facts)
		return nil
	})
	if err != nil {
		facts.ProbeError = err.Error()
	}
	return facts
}

// probeInto runs the individual probe functions against an established
// runner. Split out from Probe so tests can drive it with a fake.
func probeInto(r Runner, inv *inventory.Inventory, h *inventory.Host, f *HostFacts) {
	f.Hostname = probeHostname(r)
	f.OS, f.OSVersion = probeOS(r)
	f.Arch = probeArch(r)
	f.CPUCores = probeCPU(r)
	f.MemoryBytes = probeMemory(r)
	f.NetIfaces = probeNetwork(r)
	f.Disks = probeDisks(r, inv.Defaults.Disk.DeviceGlobs)
}

// --- individual probe functions -------------------------------------------

func probeHostname(r Runner) string {
	out, err := r.Output("hostname")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// probeOS reads /etc/os-release for the ID and VERSION_ID fields.
// Falls back to empty strings when the file isn't present (very minimal
// containers, embedded systems).
func probeOS(r Runner) (osID, osVersion string) {
	out, err := r.Output(". /etc/os-release 2>/dev/null && printf '%s\\n%s\\n' \"$ID\" \"$VERSION_ID\"")
	if err != nil {
		return "", ""
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) > 0 {
		osID = strings.TrimSpace(lines[0])
	}
	if len(lines) > 1 {
		osVersion = strings.TrimSpace(lines[1])
	}
	return
}

func probeArch(r Runner) string {
	out, err := r.Output("uname -m")
	if err != nil {
		return ""
	}
	arch := strings.TrimSpace(string(out))
	// Translate the kernel spelling to Go's GOARCH vocabulary so callers
	// can compare against runtime.GOARCH without a lookup table.
	switch arch {
	case "x86_64":
		return "amd64"
	case "aarch64":
		return "arm64"
	default:
		return arch
	}
}

func probeCPU(r Runner) int {
	out, err := r.Output("grep -c ^processor /proc/cpuinfo")
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}
	return n
}

func probeMemory(r Runner) uint64 {
	// /proc/meminfo reports MemTotal in kB; convert to bytes so callers
	// don't have to guess at units.
	out, err := r.Output("awk '/^MemTotal:/ {print $2}' /proc/meminfo")
	if err != nil {
		return 0
	}
	kb, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0
	}
	return kb * 1024
}

// probeDisks wraps disks.ListBlockDevices with the inventory-provided
// device globs (defaulting to /dev/sd + /dev/nvme, matching the existing
// prepareUnmountedDisks behavior).
func probeDisks(r Runner, globs []string) []DiskFact {
	// disks.ListBlockDevices takes an operator.CommandOperator, not our
	// narrow Runner. The real Probe flow hands it the SSH operator
	// directly; tests that exercise probeInto against a scripted Runner
	// just leave Disks empty.
	op, ok := r.(operator.CommandOperator)
	if !ok {
		return nil
	}
	prefixes := globs
	if len(prefixes) == 0 {
		prefixes = []string{"/dev/sd", "/dev/nvme"}
	} else {
		// The inventory format uses globs (/dev/sd*); lsblk parsing
		// filters by prefix. Strip any trailing '*' for the comparison.
		prefixes = make([]string, len(globs))
		for i, g := range globs {
			prefixes[i] = strings.TrimSuffix(g, "*")
		}
	}
	devs, _, err := disks.ListBlockDevices(op, prefixes)
	if err != nil {
		return nil
	}
	out := make([]DiskFact, 0, len(devs))
	for _, d := range devs {
		if d.Type != "disk" {
			continue
		}
		out = append(out, DiskFact{
			Path:       d.Path,
			Size:       d.Size,
			FSType:     d.FilesystemType,
			UUID:       d.UUID,
			MountPoint: d.MountPoint,
			Rotational: d.Rotational,
			Model:      d.Model,
		})
	}
	return out
}
