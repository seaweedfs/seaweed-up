package spec

import (
	"bytes"
	"fmt"
	"strings"
)

type MasterServerSpec struct {
	Ip                 string                 `yaml:"ip"`
	PortSsh            int                    `yaml:"port.ssh" default:"22"`
	IpBind             string                 `yaml:"ip.bind,omitempty"`
	Port               int                    `yaml:"port" default:"9333"`
	PortGrpc           int                    `yaml:"port.grpc" default:"19333"`
	VolumeSizeLimitMB  int                    `yaml:"volumeSizeLimitMB" default:"5000"`
	DefaultReplication string                 `yaml:"defaultReplication,omitempty"`
	MetricsPort        int                    `yaml:"metrics_port,omitempty"`
	Config             map[string]interface{} `yaml:"config,omitempty"`
	Arch               string                 `yaml:"arch,omitempty"`
	OS                 string                 `yaml:"os,omitempty"`
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
