package spec

import (
	"bytes"
)

// SftpServerSpec describes a SeaweedFS SFTP gateway instance. It exposes the
// SeaweedFS filer over the SFTP protocol, equivalent to running
// `weed sftp -filer=<host:port>` on the target host.
type SftpServerSpec struct {
	Ip          string                 `yaml:"ip"`
	PortSsh     int                    `yaml:"port.ssh" default:"22"`
	IpBind      string                 `yaml:"ip.bind,omitempty"`
	Port        int                    `yaml:"port" default:"2022"`
	Filer       string                 `yaml:"filer,omitempty"`
	HostKeyPath string                 `yaml:"host_key_path,omitempty"`
	AuthFile    string                 `yaml:"auth_file,omitempty"`
	MetricsPort int                    `yaml:"metrics_port,omitempty"`
	Config      map[string]interface{} `yaml:"config,omitempty"`
	Arch        string                 `yaml:"arch,omitempty"`
	OS          string                 `yaml:"os,omitempty"`
}

// WriteToBuffer emits the CLI options for `weed sftp` into buf. The provided
// masters slice is accepted for signature parity with the other component
// specs; SFTP only needs a filer endpoint, which may come from the spec or be
// derived by the caller.
func (s *SftpServerSpec) WriteToBuffer(masters []string, buf *bytes.Buffer) {
	addToBuffer(buf, "ip", s.Ip)
	addToBuffer(buf, "ip.bind", s.IpBind)
	addToBufferInt(buf, "port", s.Port, 2022)
	addToBuffer(buf, "filer", s.Filer)
	addToBuffer(buf, "sftp.host_key", s.HostKeyPath)
	addToBuffer(buf, "sftp.auth_file", s.AuthFile)
	addToBufferInt(buf, "metricsPort", s.MetricsPort, 0)
}
