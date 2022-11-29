package spec

import (
	"bytes"
	"strings"
)

type EnvoyServerSpec struct {
	Ip      string `yaml:"ip"`
	PortSsh int    `yaml:"port.ssh" default:"22"`
	Port    int    `yaml:"port" default:"9333"`
	Version string `yaml:"version,omitempty"`
}

func (f *EnvoyServerSpec) WriteToBuffer(filers []string, buf *bytes.Buffer) {
	addToBuffer(buf, "ip", f.Ip)
	addToBufferInt(buf, "port", f.Port, 8888)
	addToBuffer(buf, "filers", strings.Join(filers, ","))
}
