package spec

import (
	"bytes"
	"fmt"
	"sort"
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

// workerReservedKeys lists `weed worker` CLI option names that must not be
// set via the generic Config map because they are either derived from an
// explicit WorkerServerSpec field or managed by the deploy pipeline itself.
//
// Each entry is the exact CLI flag name (the portion after the leading `-`
// on the `weed worker` command line) as rendered by addToBuffer.
var workerReservedKeys = map[string]struct{}{
	// `admin` is always emitted by WriteToBuffer from the explicit
	// WorkerServerSpec.Admin field (or the deploy-time fallback derived
	// from the cluster's master servers). Allowing it to also appear in
	// Config would produce a duplicate `-admin` flag.
	"admin": {},
}

// WriteToBuffer writes `weed worker` CLI options to buf.
// If the worker's Admin field is empty, the first admin from admins is used.
// Additional free-form options from the Config map are emitted afterwards in
// sorted key order, skipping any keys that collide with fields already
// rendered from explicit struct fields.
func (w *WorkerServerSpec) WriteToBuffer(admins []string, buf *bytes.Buffer) {
	admin := w.Admin
	if admin == "" && len(admins) > 0 {
		admin = admins[0]
	}
	addToBuffer(buf, "admin", admin)

	if len(w.Config) == 0 {
		return
	}
	keys := make([]string, 0, len(w.Config))
	for k := range w.Config {
		if _, reserved := workerReservedKeys[k]; reserved {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(buf, "%s=%v\n", k, w.Config[k])
	}
}
