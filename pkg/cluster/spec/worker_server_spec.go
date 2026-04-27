package spec

import (
	"bytes"
	"fmt"
	"sort"
)

// WorkerServerSpec describes a SeaweedFS maintenance worker instance.
// Equivalent to running `weed worker -admin=<admin>:23646 -jobType=all`
// on the target host.
type WorkerServerSpec struct {
	Ip      string `yaml:"ip"`
	PortSsh int    `yaml:"port.ssh" default:"22"`
	Admin   string `yaml:"admin,omitempty"`
	// JobType selects which task categories or explicit handler names
	// the worker accepts from the admin's task queue. Mirrors the
	// `weed worker -jobType=<value>` flag (enterprise build): `all`,
	// `default`, `heavy`, or comma-separated explicit names like
	// `ec,balance,iceberg`. When empty, WriteToBuffer fills in
	// DefaultWorkerJobType ("all") so a worker started by
	// seaweed-up always picks up every task type the admin offers —
	// operators who want to shard task handling across worker pools
	// override per-pool. No struct-tag default: nothing in this
	// codebase reads `default:` at unmarshal time, so a tag would
	// only be decorative; the doc comment and DefaultWorkerJobType
	// are the real source of truth.
	JobType string                 `yaml:"jobType,omitempty"`
	Config  map[string]interface{} `yaml:"config,omitempty"`
	Arch    string                 `yaml:"arch,omitempty"`
	OS      string                 `yaml:"os,omitempty"`
}

// DefaultWorkerJobType is the value WriteToBuffer falls back to when
// WorkerServerSpec.JobType is empty. Matches `weed worker`'s own
// default in pluginworker, but stamping it on the rendered options
// file makes the cluster.yaml self-describing and survives a future
// upstream default change.
const DefaultWorkerJobType = "all"

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
	// `jobType` is always emitted from WorkerServerSpec.JobType (with
	// "all" as the empty-input fallback). Allowing it via Config too
	// would produce a duplicate `-jobType` flag whose ordering is
	// shell-implementation-defined.
	"jobType": {},
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

	jobType := w.JobType
	if jobType == "" {
		jobType = DefaultWorkerJobType
	}
	addToBuffer(buf, "jobType", jobType)

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
