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
	// MetricsPort exposes the Go worker's /metrics (`weed worker -metricsPort`);
	// the Rust unit has its own LanceMetricsPort so the two never share a port.
	MetricsPort int `yaml:"metrics_port,omitempty"`
	// LanceMetricsPort exposes the companion Rust unit's /metrics
	// (`weed-worker --metrics-port`); 9328 continues the per-component series.
	LanceMetricsPort int `yaml:"lance_metrics_port,omitempty"`
	// Namespace is the companion Rust weed-worker's Lance namespace URL; empty
	// derives from the first s3 server's port.lance (9101), else the unit skips.
	Namespace string `yaml:"namespace,omitempty"`
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
	addToBuffer(buf, "admin", w.adminEndpoint(admins))

	jobType := w.JobType
	if jobType == "" {
		jobType = DefaultWorkerJobType
	}
	addToBuffer(buf, "jobType", jobType)
	addToBufferInt(buf, "metricsPort", w.MetricsPort, 0)

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

// adminEndpoint is the explicit Admin field or the deploy-time default.
func (w *WorkerServerSpec) adminEndpoint(admins []string) string {
	if w.Admin != "" {
		return w.Admin
	}
	if len(admins) > 0 {
		return admins[0]
	}
	return ""
}

// WriteLanceToBuffer renders the companion Rust unit's options; the deploy
// path turns them into ExecStart flags since weed-worker reads no options file.
func (w *WorkerServerSpec) WriteLanceToBuffer(admins []string, buf *bytes.Buffer) {
	addToBuffer(buf, "admin", w.adminEndpoint(admins))
	addToBuffer(buf, "namespace", w.Namespace)
	addToBufferInt(buf, "metrics-port", w.LanceMetricsPort, 0)
	if w.LanceMetricsPort != 0 {
		// weed-worker defaults metrics to loopback; prometheus scrapes remotely.
		addToBuffer(buf, "metrics-ip", "0.0.0.0")
	}
}
