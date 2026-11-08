package spec

import (
	"bytes"
	"fmt"
	"net"
	"sort"
	"strconv"
)

// VectorServerSpec describes an S3 Vectors engine, weed-vector. It registers
// with the masters, which is how the S3 gateway finds it, and keeps the
// vector indexes' files on the filers and volume servers the masters list.
// Index and vector operations on a vector bucket need one; bucket and policy
// operations do not.
type VectorServerSpec struct {
	Ip      string `yaml:"ip"`
	PortSsh int    `yaml:"port.ssh" default:"22"`
	// Port is the engine's gRPC port; it listens on Ip, the address it
	// registers with the masters, so the gateway must be able to reach it.
	Port int `yaml:"port,omitempty"`
	// Masters is the comma-separated master list; empty derives from
	// master_servers.
	Masters string `yaml:"masters,omitempty"`
	// Config carries further weed-vector flags by name, e.g. read-cache-mb.
	Config map[string]interface{} `yaml:"config,omitempty"`
	Arch   string                 `yaml:"arch,omitempty"`
	OS     string                 `yaml:"os,omitempty"`
}

// DefaultVectorPort is weed-vector's own default gRPC port.
const DefaultVectorPort = 8090

// vectorReservedKeys are rendered from the explicit fields.
var vectorReservedKeys = map[string]struct{}{"listen": {}, "masters": {}}

// ListenAddress is the engine's gRPC address.
func (v *VectorServerSpec) ListenAddress() string {
	port := v.Port
	if port == 0 {
		port = DefaultVectorPort
	}
	return net.JoinHostPort(v.Ip, strconv.Itoa(port))
}

// WriteToBuffer writes weed-vector's options; the deploy path turns them into
// ExecStart flags, since weed-vector reads no options file.
func (v *VectorServerSpec) WriteToBuffer(buf *bytes.Buffer) {
	addToBuffer(buf, "listen", v.ListenAddress())
	addToBuffer(buf, "masters", v.Masters)
	keys := make([]string, 0, len(v.Config))
	for k := range v.Config {
		if _, reserved := vectorReservedKeys[k]; !reserved {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(buf, "%s=%v\n", k, v.Config[k])
	}
}
