package spec

import (
	"bytes"
	"strings"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/filer"
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
	Config             map[string]interface{} `yaml:"config,omitempty"`
	Arch               string                 `yaml:"arch,omitempty"`
	OS                 string                 `yaml:"os,omitempty"`
	S3                 bool                   `yaml:"s3,omitempty" default:"false"`
	S3Port             int                    `yaml:"s3.port,omitempty" default:"8333"`
	Webdav             bool                   `yaml:"webdav,omitempty" default:"false"`
	WebdavPort         int                    `yaml:"webdav.port,omitempty" default:"7333"`
}

// BackendFromConfig resolves the filer storage backend described by the
// spec's free-form Config map. It returns the default LevelDB2 backend
// when no configuration is provided. The returned Backend is already
// validated.
func (f *FilerServerSpec) BackendFromConfig() (filer.Backend, error) {
	return filer.FromConfig(f.Config)
}

func (f *FilerServerSpec) WriteToBuffer(masters []string, buf *bytes.Buffer) {
	addToBuffer(buf, "ip", f.Ip)
	addToBuffer(buf, "ip.bind", f.IpBind)
	addToBufferInt(buf, "port", f.Port, 8888)
	addToBufferInt(buf, "port.grpc", f.PortGrpc, 10000+f.Port)
	addToBuffer(buf, "master", strings.Join(masters, ","))
	addToBufferBool(buf, "s3", f.S3, false)
	addToBufferInt(buf, "s3.port", f.S3Port, 8333)
	addToBufferBool(buf, "webdav", f.Webdav, false)
	addToBufferInt(buf, "webdav.port", f.WebdavPort, 7333)
}
