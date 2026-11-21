package spec

import (
	"bytes"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
)

type MasterServerSpec struct {
	Ip                 string                 `yaml:"ip"`
	PortSsh            int                    `yaml:"port.ssh" default:"22"`
	IpBind             string                 `yaml:"ip.bind,omitempty"`
	Port               int                    `yaml:"port" default:"9333"`
	PortGrpc           int                    `yaml:"port.grpc" default:"19333"`
	VolumeSizeLimitMB  int                    `yaml:"volumeSizeLimitMB,omitempty" default:"5000"`
	DefaultReplication string                 `yaml:"defaultReplication,omitempty"`
	MetricsPort        int                    `yaml:"metrics_port,omitempty"`
	Config             map[string]interface{} `yaml:"config,omitempty"`
	Arch               string                 `yaml:"arch,omitempty"`
	OS                 string                 `yaml:"os,omitempty"`
}

// RustAddress is the master as the Rust components take it: `ip:port`, or
// `ip:port.grpc` when the gRPC port is not port+10000.
func (masterSpec *MasterServerSpec) RustAddress() string {
	port := masterSpec.Port
	if port == 0 {
		port = 9333
	}
	addr := net.JoinHostPort(masterSpec.Ip, strconv.Itoa(port))
	if masterSpec.PortGrpc != 0 && masterSpec.PortGrpc != port+10000 {
		return fmt.Sprintf("%s.%d", addr, masterSpec.PortGrpc)
	}
	return addr
}

func (masterSpec *MasterServerSpec) WriteToBuffer(masters []string, buf *bytes.Buffer) {
	addToBuffer(buf, "mdir", ".")
	addToBuffer(buf, "peers", strings.Join(masters, ","))
	addToBuffer(buf, "ip", masterSpec.Ip)
	addToBuffer(buf, "ip.bind", masterSpec.IpBind)
	addToBufferInt(buf, "port", masterSpec.Port, 9333)
	addToBufferInt(buf, "port.grpc", masterSpec.PortGrpc, 10000+masterSpec.Port)
	addToBufferInt(buf, "volumeSizeLimitMB", masterSpec.VolumeSizeLimitMB, 30000)
	addToBuffer(buf, "defaultReplication", masterSpec.DefaultReplication)
	addToBufferInt(buf, "metricsPort", masterSpec.MetricsPort, 0)

	// Free-form config entries become further master options, in sorted
	// order, as the admin's and the worker's do. Keys written from the
	// fields above are skipped rather than emitted twice, in either
	// spelling: weed's options file drops a leading dash.
	reserved := map[string]bool{
		"mdir": true, "peers": true, "ip": true, "ip.bind": true, "port": true,
		"port.grpc": true, "volumeSizeLimitMB": true, "defaultReplication": true,
		"metricsPort": true,
	}
	keys := make([]string, 0, len(masterSpec.Config))
	for k := range masterSpec.Config {
		if !reserved[strings.TrimPrefix(k, "-")] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(buf, "%s=%v\n", k, masterSpec.Config[k])
	}
}

func addToBuffer(buf *bytes.Buffer, name, value string) {
	if value != "" {
		fmt.Fprintf(buf, "%s=%s\n", name, value)
	}
}
func addToBufferInt(buf *bytes.Buffer, name string, value, defaultValue int) {
	if value != 0 && value != defaultValue {
		fmt.Fprintf(buf, "%s=%d\n", name, value)
	}
}
func addToBufferBool(buf *bytes.Buffer, name string, value, defaultValue bool) {
	if value != defaultValue {
		fmt.Fprintf(buf, "%s=%v\n", name, value)
	}
}
