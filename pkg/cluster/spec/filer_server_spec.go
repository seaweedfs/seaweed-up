package spec

import (
	"bytes"
	"strings"
)

type FilerServerSpec struct {
	Ip                 string                 `yaml:"ip"`
	PortSsh            int                    `yaml:"port.ssh" default:"22"`
	IpBind             string                 `yaml:"ip.bind,omitempty"`
	IpPublic           string                 `yaml:"ip.public,omitempty"`
	Port               int                    `yaml:"port" default:"9333"`
	PortGrpc           int                    `yaml:"port.grpc" default:"19333"`
	PortPublic         int                    `yaml:"port.public,omitempty"`
	DataCenter         string                 `yaml:"dataCenter,omitempty"`
	Rack               string                 `yaml:"rack,omitempty"`
	DefaultReplication int                    `yaml:"defaultReplication,omitempty"`
	MetricsPort        int                    `yaml:"metrics_port,omitempty"`
	ConfigDir          string                 `yaml:"dir.conf,omitempty" default:"/etc/seaweed"`
	DataDir            string                 `yaml:"dir.data,omitempty" default:"/opt/seaweed"`
	Config             map[string]interface{} `yaml:"config,omitempty"`
	Arch               string                 `yaml:"arch,omitempty"`
	OS                 string                 `yaml:"os,omitempty"`
}

func (f *FilerServerSpec) WriteToBuffer(masters []string, buf *bytes.Buffer) {
	addToBuffer(buf, "ip", f.Ip)
	addToBuffer(buf, "ip.bind", f.IpBind)
	addToBufferInt(buf, "port", f.Port, 8888)
	addToBufferInt(buf, "port.grpc", f.PortGrpc, 10000+f.Port)
	addToBuffer(buf, "master", strings.Join(masters, ","))
}
