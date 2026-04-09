package spec

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
)

// AdminServerSpec describes a SeaweedFS `weed admin` UI instance.
//
// It is equivalent to running:
//
//	weed admin -port=23646 -master=<m1:9333,m2:9333>
//
// The admin UI serves an HTTP management console for the cluster and
// defaults to port 23646. Filers are auto-discovered via the masters,
// so there is no explicit filer flag.
type AdminServerSpec struct {
	Ip            string                 `yaml:"ip"`
	PortSsh       int                    `yaml:"port.ssh" default:"22"`
	IpBind        string                 `yaml:"ip.bind,omitempty"`
	Port          int                    `yaml:"port" default:"23646"`
	Masters       []string               `yaml:"masters,omitempty"`
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
// Only flags actually accepted by `weed admin` are written. Filers are
// discovered automatically from the masters, so no filer flag is emitted.
// If the spec declares its own Masters override they take precedence over
// the cluster masters argument. Any entries in Config are written last as
// free-form key=value extensions, allowing users to pass additional flags
// (e.g. `port.grpc`, `adminPassword`) without requiring a struct field.
func (a *AdminServerSpec) WriteToBuffer(masters []string, buf *bytes.Buffer) {
	addToBuffer(buf, "ip", a.Ip)
	addToBuffer(buf, "ip.bind", a.IpBind)
	addToBufferInt(buf, "port", a.Port, 23646)

	mastersToUse := masters
	if len(a.Masters) > 0 {
		mastersToUse = a.Masters
	}
	addToBuffer(buf, "master", strings.Join(mastersToUse, ","))

	addToBuffer(buf, "dataDir", a.DataDir)
	addToBuffer(buf, "adminUser", a.AdminUser)
	addToBuffer(buf, "adminPassword", a.AdminPassword)

	// Write free-form Config entries in stable (sorted) order so the
	// resulting options file is deterministic.
	if len(a.Config) > 0 {
		keys := make([]string, 0, len(a.Config))
		for k := range a.Config {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(buf, "%s=%v\n", k, a.Config[k])
		}
	}
}
