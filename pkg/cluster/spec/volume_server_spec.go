package spec

import (
	"bytes"
	"strconv"
	"strings"
)

type VolumeServerSpec struct {
	Ip                 string                 `yaml:"ip"`
	PortSsh            int                    `yaml:"port.ssh" default:"22"`
	IpBind             string                 `yaml:"ip.bind,omitempty"`
	IpPublic           string                 `yaml:"ip.public,omitempty"`
	Port               int                    `yaml:"port" default:"9333"`
	PortGrpc           int                    `yaml:"port.grpc" default:"19333"`
	PortPublic         int                    `yaml:"port.public,omitempty"`
	Folders            []*FolderSpec          `yaml:"folders"`
	DataCenter         string                 `yaml:"dataCenter,omitempty"`
	Rack               string                 `yaml:"rack,omitempty"`
	DefaultReplication int                    `yaml:"defaultReplication,omitempty"`
	MetricsPort        int                    `yaml:"metrics_port,omitempty"`
	ConfigDir          string                 `yaml:"conf_dir,omitempty" default:"/etc/seaweed"`
	DataDir            string                 `yaml:"data_dir,omitempty" default:"/opt/seaweed"`
	Config             map[string]interface{} `yaml:"config,omitempty"`
	Arch               string                 `yaml:"arch,omitempty"`
	OS                 string                 `yaml:"os,omitempty"`
}
type FolderSpec struct {
	Folder   string `yaml:"folder"`
	DiskType string `yaml:"disk" default:"hdd"`
	Max      int    `yaml:"max,omitempty"`
}

func (vs *VolumeServerSpec) WriteToBuffer(masters []string, buf *bytes.Buffer) {
	addToBuffer(buf, "ip", vs.Ip)
	addToBuffer(buf, "ip.bind", vs.IpBind)
	addToBufferInt(buf, "port", vs.Port, 8888)
	addToBufferInt(buf, "port.grpc", vs.PortGrpc, 10000+vs.Port)
	addToBuffer(buf, "mserver", strings.Join(masters, ","))
	var dirs, disks, maxes []string
	for _, folder := range vs.Folders {
		dirs = append(dirs, folder.Folder)
		diskType := "hdd"
		if folder.DiskType != "" {
			diskType = folder.DiskType
		}
		disks = append(disks, diskType)
		maxes = append(maxes, strconv.Itoa(folder.Max))
	}
	addToBuffer(buf, "dir", strings.Join(dirs, ","))
	addToBuffer(buf, "max", strings.Join(maxes, ","))
	addToBuffer(buf, "disks", strings.Join(disks, ","))
}
