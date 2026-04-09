package spec

import (
	"bytes"
)

// WorkerServerSpec describes a SeaweedFS maintenance worker instance.
// Equivalent to running `weed worker -admin=<admin>:23646` on the target host.
type WorkerServerSpec struct {
	Ip      string                 `yaml:"ip"`
	PortSsh int                    `yaml:"port.ssh" default:"22"`
	Admin   string                 `yaml:"admin,omitempty"`
	Config  map[string]interface{} `yaml:"config,omitempty"`
	Arch    string                 `yaml:"arch,omitempty"`
	OS      string                 `yaml:"os,omitempty"`
}

// WriteToBuffer writes `weed worker` CLI options to buf.
// If the worker's Admin field is empty, the first admin from admins is used.
func (w *WorkerServerSpec) WriteToBuffer(admins []string, buf *bytes.Buffer) {
	admin := w.Admin
	if admin == "" && len(admins) > 0 {
		admin = admins[0]
	}
	addToBuffer(buf, "admin", admin)
}
