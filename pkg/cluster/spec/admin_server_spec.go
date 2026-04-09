package spec

import (
	"bytes"
	"strings"
)

// AdminServerSpec describes a SeaweedFS `weed admin` UI instance.
//
// It is equivalent to running:
//
//	weed admin -port=23646 -masters=<m1:9333,m2:9333> [-filer=<host:port>]
//
// The admin UI serves an HTTP management console for the cluster and
// defaults to port 23646.
type AdminServerSpec struct {
	Ip            string                 `yaml:"ip"`
	PortSsh       int                    `yaml:"port.ssh" default:"22"`
	IpBind        string                 `yaml:"ip.bind,omitempty"`
	Port          int                    `yaml:"port" default:"23646"`
	Masters       []string               `yaml:"masters,omitempty"`
	Filer         string                 `yaml:"filer,omitempty"`
	DataDir       string                 `yaml:"dataDir,omitempty"`
	AdminUser     string                 `yaml:"admin_user,omitempty"`
	AdminPassword string                 `yaml:"admin_password,omitempty"`
	Config        map[string]interface{} `yaml:"config,omitempty"`
	Arch          string                 `yaml:"arch,omitempty"`
	OS            string                 `yaml:"os,omitempty"`
}

// WriteToBuffer writes the `weed admin` CLI options into buf, one per line in
// `key=value` form (consumed via `-options=<file>`).
//
// If the spec declares its own Masters override they take precedence over the
// cluster masters argument.
func (a *AdminServerSpec) WriteToBuffer(masters []string, buf *bytes.Buffer) {
	addToBuffer(buf, "ip", a.Ip)
	addToBuffer(buf, "ip.bind", a.IpBind)
	addToBufferInt(buf, "port", a.Port, 23646)

	mastersToUse := masters
	if len(a.Masters) > 0 {
		mastersToUse = a.Masters
	}
	addToBuffer(buf, "masters", strings.Join(mastersToUse, ","))

	addToBuffer(buf, "filer", a.Filer)
	addToBuffer(buf, "dataDir", a.DataDir)
	addToBuffer(buf, "adminUser", a.AdminUser)
	addToBuffer(buf, "adminPassword", a.AdminPassword)
}
