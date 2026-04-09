package spec

import (
	"bytes"
)

// S3ServerSpec describes a standalone SeaweedFS S3 gateway instance.
// It is deployed as a separate process running `weed s3`, talking to an
// existing filer endpoint.
type S3ServerSpec struct {
	Ip          string                 `yaml:"ip"`
	PortSsh     int                    `yaml:"port.ssh" default:"22"`
	IpBind      string                 `yaml:"ip.bind,omitempty"`
	Port        int                    `yaml:"port" default:"8333"`
	PortGrpc    int                    `yaml:"port.grpc,omitempty"`
	MetricsPort int                    `yaml:"metrics_port,omitempty"`
	// Filer is the ip:port of the filer this gateway connects to. If empty,
	// the deploy logic will default it to the first filer in the spec.
	Filer string `yaml:"filer,omitempty"`
	Arch  string `yaml:"arch,omitempty"`
	OS    string `yaml:"os,omitempty"`
	// S3Config is the contents of s3.json (IAM / identities). When set, it
	// is uploaded next to s3.options and wired in via -config.
	S3Config map[string]interface{} `yaml:"s3_config,omitempty"`
}

// WriteToBuffer renders the `weed s3` CLI options file. If s3ConfigPath is
// non-empty, it is wired in as the -config option (path to s3.json).
func (s *S3ServerSpec) WriteToBuffer(buf *bytes.Buffer, s3ConfigPath string) {
	addToBuffer(buf, "ip", s.Ip)
	addToBuffer(buf, "ip.bind", s.IpBind)
	addToBufferInt(buf, "port", s.Port, 8333)
	addToBufferInt(buf, "port.grpc", s.PortGrpc, 0)
	addToBufferInt(buf, "metricsPort", s.MetricsPort, 0)
	addToBuffer(buf, "filer", s.Filer)
	if s3ConfigPath != "" {
		addToBuffer(buf, "config", s3ConfigPath)
	}
}
