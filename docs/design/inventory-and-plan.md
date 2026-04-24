# Design: inventory → plan → deploy

Status: draft · Owner: @chrislusf · Last updated: 2026-04-24

## Summary

Add one command — `seaweed-up cluster plan` — so operators don't have
to hand-author a full cluster YAML before their first deploy. It grows
in capability across three phases; in all phases it reads the same
inventory file:

- **Phase 1** (`--json`, probe-only) — SSH to each host in the
  inventory and emit hardware facts (disks, CPU, memory, network) as
  JSON.
- **Phase 2** (`-o cluster.yaml`, greenfield synthesis) — same probe,
  plus synthesis of a reviewable `cluster.yaml` that the existing
  `cluster deploy` command consumes unchanged.
- **Phase 3** (`-o cluster.yaml`, append-merge) — re-runs after
  growing the inventory leave every existing entry in
  `cluster.yaml` byte-identical and only append new hosts.

Mirrors Terraform's model where `plan` subsumes the refresh step —
users learn one verb.

The inventory file is deliberately minimal: hosts, roles, SSH settings,
and a couple of templating knobs. Everything else is discovered on the
host or derived from defaults.

Re-running `plan` after growing the inventory only *appends* to the
generated `cluster.yaml`. No existing entry is reordered, no comment is
clobbered, no manual tuning is lost.

## Goals

- Onboarding flow from zero to a deployable cluster YAML without reading
  the full schema.
- A reviewable intermediate artifact (`cluster.yaml`) that the operator
  can hand-edit before `deploy`. The review step is the feature — this is
  not a plan-and-apply auto-pilot.
- Incremental growth: add a host to the inventory, re-run `plan`, only
  the new host's spec shows up in the diff.
- Output format is the existing `pkg/cluster/spec.Specification`. No
  flag-day migration; existing hand-authored `cluster.yaml` files
  continue to work untouched.

## Non-goals (initial cut)

- Auto-removal of hosts. Deleting a host from the inventory does **not**
  remove it from `cluster.yaml`; `plan` emits a warning only. Removal is
  destructive and the user owns it.
- Topology inference. `plan` does not decide that 3 hosts should be
  masters or that the 4th should be a filer. Roles are assigned
  explicitly in the inventory.
- Replacing `cluster deploy`. `plan` is strictly additive. A user who
  prefers to hand-write the cluster YAML keeps doing that.

## User flow

### First deploy

```
# 1. write a minimal inventory
$ $EDITOR inventory.yaml

# 2. probe hosts, generate cluster.yaml for review
$ seaweed-up cluster plan -i inventory.yaml -o cluster.yaml
  probing 10.0.0.11 ... ok (24 cores, 64 GiB, 0 free disks)
  probing 10.0.0.21 ... ok (32 cores, 128 GiB, 4 free disks)
  probing 10.0.0.99 ... FAIL: dial tcp: i/o timeout
  generated cluster.yaml (3 masters, 3 volumes, 2 filers; 1 host skipped)

# 3. review, fill in placeholders (e.g. filer secrets), hand-edit any tuning
$ $EDITOR cluster.yaml

# 4. deploy as today
$ seaweed-up cluster deploy -f cluster.yaml
```

### Grow the cluster

```
# add a new host to inventory.yaml
$ $EDITOR inventory.yaml

# re-run plan; merges into the existing cluster.yaml
$ seaweed-up cluster plan -i inventory.yaml -o cluster.yaml
  probing 10.0.0.24 ... ok (new volume host, 6 disks)
  appended 1 volume_server to cluster.yaml (0 existing entries changed)

# deploy is idempotent for existing hosts and rolls out the new one
$ seaweed-up cluster deploy -f cluster.yaml
```

## Inventory file

```yaml
# inventory.yaml — the full schema.

defaults:
  ssh:
    user: ubuntu
    port: 22
    identity: ~/.ssh/id_rsa
  disk:
    device_globs: ["/dev/sd*", "/dev/nvme*"]   # candidate disks
    exclude:      ["/dev/sda"]                  # boot disk, etc.
    reserve_pct:  5                             # headroom; capped at 10 GiB
    disk_type_auto: true                        # rota → hdd, !rota → ssd

hosts:
  - ip: 10.0.0.11
    roles: [master, filer]
    labels: { zone: a, rack: r1 }

  - ip: 10.0.0.12
    roles: [master, filer]
    ssh: { user: deploy }                       # host-level override
    labels: { zone: b, rack: r2 }

  - ip: 10.0.0.13
    roles: [master]

  - ip: 10.0.0.21
    roles: [volume]
    labels: { zone: a, rack: r1 }

  - ip: 10.0.0.71
    roles: [admin, worker]

  # Filer metadata store — not SSH-managed, never probed.
  # Declared so --filer-backend can reference it by tag.
  - ip: 10.0.0.41
    roles: [external]
    tag: postgres-metadata
```

Recognized roles: `master`, `volume`, `filer`, `s3`, `sftp`, `admin`,
`worker`, `envoy`, `external`. A host may have multiple roles; `plan`
emits one entry per role into the corresponding `*_servers` section.

Per-host `ssh:` and `labels:` override `defaults`. `labels` map onto
`DataCenter` / `Rack` on the volume spec (and future filer/s3 fields as
they grow).

## Probe

Single SSH session per host, several commands batched:

- `lsblk -b -P -o KNAME,PATH,SIZE,UUID,FSTYPE,TYPE,MOUNTPOINT,ROTA,MODEL`
  reusing and extending `pkg/disks/disks.go` (adds `ROTA`, `MODEL` to
  `BlockDevice`).
- `cat /proc/cpuinfo | grep -c ^processor`
- `awk '/MemTotal/{print $2}' /proc/meminfo`
- Network: prefer `ip -j addr` (iproute2 ≥ 4.15, shipped on every
  distro released after early 2018). Detect JSON support with
  `ip -j addr 2>/dev/null` and fall back to parsing text-mode `ip addr`
  when it's missing — we still care about older LTS images like CentOS
  7 and Ubuntu 16.04. Link speed comes from
  `cat /sys/class/net/$if/speed` per non-lo iface.
- `. /etc/os-release; echo "$ID $VERSION_ID"`
- `uname -m`

Collected into `HostFacts`:

```go
type HostFacts struct {
    IP          string
    Hostname    string
    OS          string       // "ubuntu"
    OSVersion   string       // "22.04"
    Arch        string       // "amd64"
    CPUCores    int
    MemoryBytes uint64
    NetIfaces   []NetIface
    Disks       []DiskFacts
    ProbedAt    time.Time
    ProbeError  string       // non-empty when the host is unreachable
}

type DiskFacts struct {
    Path       string   // /dev/sdb
    Size       uint64
    FSType     string   // "" when unformatted
    UUID       string
    MountPoint string   // "" when unmounted
    Rotational bool     // → disk_type
    Model      string   // audit/debug comment in output
}
```

Probe failures are per-host and non-fatal: a warning is printed to
stderr, and a `HostFacts` entry is still emitted with `ProbeError` set
and the other fields left at their zero values. Emitting the failed
entry (rather than dropping it) keeps the JSON contract 1:1 with the
deduplicated set of SSH targets produced by `ProbeHosts` —
one record per `ip:ssh-port` actually probed. Downstream scripts can
distinguish "target is unreachable" from "target is absent from the
probe plan", and Phase 2 `plan` can decide per-role whether to skip
the host, fail fast, or retain a stale entry. Re-running picks it up
once reachable.

`cluster plan -i inventory.yaml --json` prints the `HostFacts` slice
and exits. Handy for scripting, and — until Phase 2 lands the
`-o cluster.yaml` synthesis mode — the primary way to see what the
planner will observe.

## Plan: generation

For each inventory host (skipping `external` hosts during probe):

- **master**: append `MasterServerSpec{Ip, Port: 9333, PortSsh: …}`.
- **volume**: append `VolumeServerSpec{Ip, Port: 8080, Folders: derive(facts.Disks)}`.
  - `derive` applies the same selection rules as today's
    `prepareUnmountedDisks`: skip partitioned parents, skip mounted,
    skip `defaults.disk.exclude` globs. For each eligible disk emit
    `FolderSpec{Folder: "/data<N>", DiskType, Max}`.
  - `DiskType` comes from `defaults.disk.disk_type_auto` — `hdd` when
    `Rotational`, otherwise `ssd`.
  - `Max` is the maximum volume count for the folder. `Size` from
    `DiskFacts` is in bytes; `volumeSizeLimitMB` from `GlobalOptions`
    is in MiB. Conversion is explicit:

    ```
    sizeMiB    = Size / (1024 * 1024)
    reserveMiB = min(sizeMiB * reserve_pct / 100, 10 * 1024)   // cap 10 GiB
    usableMiB  = sizeMiB - reserveMiB
    Max        = usableMiB / volumeSizeLimitMB                 // integer div
    ```

    The reserve rule (min of 5 % and 10 GiB) is consistent with the
    PR #4 proposal. `volumeSizeLimitMB` is read from (in priority
    order): a `--volume-size-limit-mb` CLI flag on `plan`, the
    `global.volumeSizeLimitMB` field in the existing `cluster.yaml`
    when we're merging, the `GlobalOptions` struct default (5000)
    for greenfield runs.
  - `plan` does **not** mkfs, format, or mount. It predicts the target
    layout. The existing `deploy --mount-disks` path performs the
    filesystem operations.
- **filer**: append `FilerServerSpec{Ip, Port: 8888}`. Plan writes the
  port explicitly (`FilerServerSpec.Port`'s `default:"9333"` struct tag
  is a stale annotation — no defaults library consumes it; the real
  runtime fallback in `WriteToBuffer` is 8888). If `--filer-backend` is
  supplied, also populate `config:` (see below); otherwise emit a TODO
  placeholder comment.
- **s3 / sftp / admin / worker / envoy**: synthesize the matching spec
  with defaults. Role-specific required fields that cannot be inferred
  (e.g. `admin_password`) emit as `CHANGE_ME` with a comment, matching
  the convention in `examples/typical.yaml`.

### Greenfield `global:` section

When `cluster.yaml` doesn't exist yet, `plan` emits an explicit `global:`
block populated from the `GlobalOptions` struct defaults, rather than
omitting the block and relying on future deploy-time fallbacks:

```yaml
global:
  dir.conf: /etc/seaweed
  dir.data: /opt/seaweed
  volumeSizeLimitMB: 5000
  replication: "000"
```

Emitting the block explicitly makes the plan reproducible and gives
operators an obvious place to tune values before `deploy`. During
merge, an existing `global:` block is left untouched — including any
hand-edits — because merge never rewrites existing nodes.

### `--filer-backend` (optional)

Three equivalent ways to supply the DSN, in priority order:

```
# 1. file (recommended: avoids leaking the password via `ps`)
seaweed-up cluster plan -i inventory.yaml -o cluster.yaml \
    --filer-backend-file /etc/seaweed-up/filer.dsn

# 2. environment variable (good for CI)
SEAWEEDUP_FILER_BACKEND='postgres://seaweed:s3cret@10.0.0.41/seaweedfs' \
    seaweed-up cluster plan -i inventory.yaml -o cluster.yaml

# 3. direct CLI flag (convenient, but note the security caveat below)
seaweed-up cluster plan -i inventory.yaml -o cluster.yaml \
    --filer-backend 'postgres://seaweed:CHANGE_ME@10.0.0.41:5432/seaweedfs?sslmode=disable'
```

> ⚠ `--filer-backend` passes the password through `os.Args`, which is
> world-readable via `/proc/<pid>/cmdline` and `ps` while the process is
> alive. Prefer `--filer-backend-file` or `SEAWEEDUP_FILER_BACKEND`
> whenever the DSN carries a real secret. The CLI flag remains available
> for throwaway / dev setups where the convenience wins.

Whichever form is used, the value is parsed as a DSN and expanded into
the per-filer `config:` block:

```yaml
filer_servers:
  - ip: 10.0.0.11
    port: 8888
    config:
      type: postgres
      hostname: 10.0.0.41
      port: 5432
      username: seaweed
      password: CHANGE_ME
      database: seaweedfs
      sslmode: disable
```

Supported schemes in the first cut:

- `postgres://user:pass@host:port/db?sslmode=…`
- `mysql://user:pass@host:port/db`
- `redis://:pass@host:port/db` and `redis://host:port/db`

Unrecognized schemes produce an error listing the supported set. The
user can always hand-write the `config:` block after `plan`.

### Labels

Per-host `labels.zone` / `labels.rack` map to `DataCenter` / `Rack` on
the volume server spec. Arbitrary other labels are preserved as a
commented-out `# labels: { foo: bar }` annotation above the host's
entry, for operator reference.

## Plan: merge semantics

This is the load-bearing piece of the design. The requirement:

> Adding a host to the inventory and re-running `plan` must not move or
> modify any existing entry in `cluster.yaml`.

### Approach

Parse `cluster.yaml` with `gopkg.in/yaml.v3`'s `yaml.Node` API. `yaml.Node`
preserves, across a parse → edit → encode round-trip:

- Head, line, and foot comments
- Key order within a mapping
- Item order within a sequence
- Inline vs. block style of individual nodes

Treat the parsed tree as mutable state. Never `yaml.Marshal(spec)` an
existing file — that round-trips through Go structs and loses comments,
field order, and whatever style choices the operator made.

### Keying

An entry in a `*_servers` section is identified by `ip:port` — the key
that `deploy` already uses for state and reconciliation. The `port`
field in the key is the spec's service port (`Port`), not the SSH port.
The inventory does not carry a per-host service-port override:
`cluster plan` emits the role's well-known default (9333, 8888, 8080,
…) unless the operator later hand-edits `cluster.yaml`. That keeps the
inventory minimal and avoids overloading a single `port:` field across
roles that have different service ports.

Inventories are host-centric; a host with `roles: [master, filer]`
produces two entries, in two different sections, both keyed at
`<ip>:<role's default port>`. Multi-process-per-host (e.g. several
volume processes sharing a physical server) is a planner concern —
nothing in Phase 1 emits it; a later phase can add a flag like
`--volumes-per-host N` that synthesizes multiple entries with
non-default ports.

### Rules

```
for section in (master_servers, volume_servers, filer_servers, …):
    existing = index of existing entries by ip:port
    for host in inventory with that role:
        key = ip ":" role-default-port
        if key in existing:
            # never touch. if probed facts have drifted, emit a
            # warning-only report ("10.0.0.21:8080 now has 6 disks,
            # your cluster.yaml lists 4"). do not mutate the entry.
            continue
        else:
            append a newly-generated node to the end of the section's
            sequence

for entry in existing but not in inventory:
    emit a warning on stderr. do not remove the entry.
```

### Stability guarantees (testable)

- **No-op run** (inventory unchanged): output bytes equal input bytes,
  byte-for-byte. Golden-file test.
- **Append run** (inventory has +1 host): the textual diff between input
  and output is exactly a new mapping block at the appropriate section's
  tail. No whitespace changes anywhere else. Golden-file test.
- **User-edit survival**: an operator edits `max: 200` into an existing
  volume entry; re-running `plan` leaves the edit in place. Golden-file
  test.

### Refresh (deferred)

`plan --refresh-host=10.0.0.21` rebuilds only that one host's entry from
fresh facts. Off by default; explicit opt-in. Deferred to Phase 4.

## Edge cases

| Case | Behavior |
| --- | --- |
| Host unreachable during probe | Skip; log warning on stderr; leave `cluster.yaml` untouched for that host. Re-runnable. |
| Host in inventory has no role | Error at inventory-parse time. |
| Duplicate (ip, role) in inventory | Error at inventory-parse time. Duplicate IPs across different roles (a colocated master+filer, say) are allowed. |
| Host IP changed (old removed, new added) | Plan sees a new host and an orphaned one. New appended; orphan warned about. No auto-migration. |
| Role=volume but no free disks found | Emit `volume_server` entry with `folders: []` plus a `# no free disks found on $host` comment. `deploy` will fail validation — problem surfaces at plan time, not mid-deploy. |
| `cluster.yaml` does not yet exist | Generate from scratch. Header comment: `# Generated by seaweed-up plan from inventory.yaml. Safe to hand-edit.` |
| `-o` points to a file with a different `cluster_name` | Error: "cluster name mismatch"; require `--force` to overwrite. |
| Probed disk is already mounted at `/data<N>` | Skip re-provisioning; emit the folder in the spec using the existing mountpoint. Matches current `prepareUnmountedDisks` skip-on-mount behavior. |
| `plan --dry-run` | Print textual diff against `-o` to stdout; write nothing. |

## Code layout

```
cmd/
  cluster_plan.go            # unified command. flags: -i, -o, --json,
                             #   --dry-run, --refresh-host, --filer-backend,
                             #   --filer-backend-file, --volume-size-limit-mb,
                             #   --concurrency. Phase 1 implements --json only;
                             #   Phase 2 adds -o; Phase 3 adds --dry-run.

pkg/cluster/
  inventory/
    inventory.go             # type, load+validate
    inventory_test.go
  probe/
    probe.go                 # HostFacts, orchestrator
    disks.go                 # wraps pkg/disks
    network.go
    sysinfo.go               # cpu, memory, os
    probe_test.go
  plan/
    generate.go              # inventory + facts → *yaml.Node
    merge.go                 # append-merge into existing *yaml.Node
    filer_backend.go         # DSN → config: block
    plan_test.go
    testdata/golden/
      add_volume_host.before.yaml
      add_volume_host.after.yaml
      preserve_user_max.before.yaml
      preserve_user_max.after.yaml
      no_op.before.yaml
      no_op.after.yaml

pkg/disks/
  disks.go                   # extend BlockDevice: add Rotational, Model
```

## Phased rollout

1. **Phase 1: plan --json (probe-only).** `seaweed-up cluster plan -i inventory.yaml --json`.
   Prints `HostFacts` per host. Read-only on hosts; purely additive in
   the codebase. Validates SSH/probe plumbing before anyone depends on
   it. The command is named `plan` from day one (mirroring Terraform's
   model where refresh is subsumed into plan); in Phase 1 it only does
   the probe step. Deliverable: one PR.
2. **Phase 2: plan (greenfield).** `plan -o cluster.yaml` when `-o` does
   not yet exist. Full generation, no merge yet. Golden tests for 1-host
   dev, 3+3+3 typical, 5-node mixed. Deliverable: one PR.
3. **Phase 3: plan (append-merge).** The `yaml.Node` merge. Risky piece;
   heavy test coverage on the stability guarantees above. This is the
   PR that unlocks the grow-the-cluster workflow. Deliverable: one PR.
4. **Phase 4: ergonomics.** `plan --dry-run` diff output, `--refresh-host`,
   inventory tag references (`tag: postgres-metadata` → DSN rewrite).
   Deferred until real use reveals what's missing.

Phases 1–3 are the minimum product. Phase 4 is ice cream.

## Resolved design decisions

1. **Role assignment: inventory-only.** No heuristics. Every host
   declares its roles explicitly.
2. **`FolderSpec` is not extended.** No `block_device` / `uuid` fields in
   the YAML. Folder path remains the sole key; UUID is rediscovered at
   deploy time via `blkid` (already the case post-#65).
3. **Filer metadata store: DSN via flag, file, or env var.** When
   absent, `plan` emits a placeholder `# TODO: config:` block per filer.
   When present, the DSN is parsed and expanded into each filer's
   `config:` section. Three input forms:
   `--filer-backend-file` (recommended — no `ps` leak),
   `SEAWEEDUP_FILER_BACKEND` env var, or direct `--filer-backend` flag
   (convenient but leaks the DSN via `/proc/<pid>/cmdline`).
4. **Merge key is `ip:port` in `cluster.yaml`, but inventory carries
   no service-port field.** The cluster.yaml spec already uses
   `ip:port` and `plan` preserves that. The inventory stays minimal:
   it does not carry a per-host service-port override. `plan` emits
   the role's well-known default (9333, 8888, 8080, …) on synthesis,
   and if an operator later wants multiple volume processes on one
   host they hand-edit `cluster.yaml` or wait for a future
   `--volumes-per-host` flag. Keeps inventory host-centric and avoids
   overloading one `port:` field across roles with different service
   ports.
5. **`Max` formula makes units explicit.** `Size` from probe is bytes,
   `volumeSizeLimitMB` is MiB; convert via `Size / (1024 * 1024)`
   before the divide. The reserve is `min(5 %, 10 GiB)` as proposed in
   #4. `volumeSizeLimitMB` precedence:
   `--volume-size-limit-mb` flag → existing `cluster.yaml`'s
   `global.volumeSizeLimitMB` on merge → `GlobalOptions` default 5000
   on greenfield.
6. **Greenfield `global:` is emitted explicitly.** Rather than omit the
   block and rely on deploy-time fallbacks, `plan` writes the defaults
   (`dir.conf`, `dir.data`, `volumeSizeLimitMB`, `replication`) into
   the generated YAML so the spec is self-describing and the operator
   has an obvious tuning surface.
7. **Network probe degrades gracefully.** Prefer `ip -j addr` (JSON,
   iproute2 ≥ 4.15); fall back to parsing `ip addr` text output so
   older LTS images (CentOS 7, Ubuntu 16.04) still probe.

## Open questions

None blocking Phase 1. Flag as they come up during implementation.
