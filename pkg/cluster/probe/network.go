package probe

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// probeNetwork returns the host's non-loopback interfaces with their
// addresses and link speeds.
//
// Two-path implementation: prefer `ip -j addr` (iproute2 >= 4.15, which
// is every distro released after early 2018), fall back to parsing
// text-mode `ip addr` for older LTS images (CentOS 7, Ubuntu 16.04).
// JSON mode is detected by running the command once and checking whether
// the output is valid JSON; an empty or non-JSON response triggers the
// fallback.
func probeNetwork(r Runner) []NetIface {
	if ifaces := probeNetworkJSON(r); ifaces != nil {
		return ifaces
	}
	return probeNetworkText(r)
}

// ipJSONEntry is the subset of `ip -j addr` we consume. The full schema
// has many more fields (operstate, flags, stats, etc.); we only need
// name and addresses. Link speed comes from sysfs in both paths.
type ipJSONEntry struct {
	Ifname   string `json:"ifname"`
	AddrInfo []struct {
		Family string `json:"family"`
		Local  string `json:"local"`
	} `json:"addr_info"`
}

func probeNetworkJSON(r Runner) []NetIface {
	out, err := r.Output("ip -j addr 2>/dev/null")
	if err != nil || len(out) == 0 {
		return nil
	}
	var entries []ipJSONEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil
	}
	return materializeIfaces(r, entries)
}

// ipAddrTextRE matches "    inet 10.0.0.1/24 ..." lines.
var ipAddrTextRE = regexp.MustCompile(`^\s+inet6?\s+([0-9a-fA-F:.]+)(?:/\d+)?\s`)

// ifnameLineRE matches the leading "2: eth0: <BROADCAST,...>" header
// that introduces each interface block in `ip addr` text output.
var ifnameLineRE = regexp.MustCompile(`^\d+:\s+([^:@]+)(?:@[^:]+)?:\s`)

func probeNetworkText(r Runner) []NetIface {
	out, err := r.Output("ip addr")
	if err != nil {
		return nil
	}
	var (
		result  []NetIface
		current *NetIface
	)
	flush := func() {
		if current != nil && current.Name != "" && current.Name != "lo" {
			result = append(result, *current)
		}
	}
	for _, line := range strings.Split(string(out), "\n") {
		if m := ifnameLineRE.FindStringSubmatch(line); m != nil {
			flush()
			name := strings.TrimSpace(m[1])
			current = &NetIface{Name: name}
			continue
		}
		if current == nil {
			continue
		}
		if m := ipAddrTextRE.FindStringSubmatch(line); m != nil {
			current.Addresses = append(current.Addresses, m[1])
		}
	}
	flush()
	for i := range result {
		result[i].SpeedMbps = readSpeed(r, result[i].Name)
	}
	return result
}

// materializeIfaces turns the JSON entries into NetIface values, dropping
// the loopback and attaching sysfs-reported link speeds.
func materializeIfaces(r Runner, entries []ipJSONEntry) []NetIface {
	out := make([]NetIface, 0, len(entries))
	for _, e := range entries {
		if e.Ifname == "" || e.Ifname == "lo" {
			continue
		}
		iface := NetIface{Name: e.Ifname}
		for _, a := range e.AddrInfo {
			if a.Local != "" {
				iface.Addresses = append(iface.Addresses, a.Local)
			}
		}
		iface.SpeedMbps = readSpeed(r, e.Ifname)
		out = append(out, iface)
	}
	return out
}

// readSpeed reads /sys/class/net/<iface>/speed. The kernel reports -1
// (or an error) for interfaces with no physical link — virtual NICs,
// bridges, some hypervisor drivers. We return 0 in those cases rather
// than surfacing the -1 to callers.
func readSpeed(r Runner, iface string) int {
	// Shell-quote the interface name to avoid injection (hostile inventory
	// isn't a real threat here, but cheap to guard).
	cmd := "cat /sys/class/net/" + shellQuote(iface) + "/speed 2>/dev/null"
	out, err := r.Output(cmd)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// shellQuote wraps s in single quotes, escaping any embedded quotes so
// it is safe to splice into a POSIX shell command.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
