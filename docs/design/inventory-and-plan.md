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
    device_globs: ["/dev/sd*", "/dev/nvme*", "/dev/xvd*", "/dev/vd*"]  # candidate disks
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
to stdout and exits — handy for scripting and for seeing what the
planner observed.

When `-o cluster.yaml` is set, the same `HostFacts` slice is also
written as a sidecar JSON file alongside the synthesized YAML
(`cluster.yaml` → `cluster.facts.json`). The sidecar gives operators
a record of what the probe saw without re-running it, and feeds
Phase 4 drift detection: each plan run loads the previous sidecar
*before* the fresh probe writes its replacement, then `plan.DetectDrift`
compares per-host disk paths and surfaces a `WARN: drift on <host>` line
when the set has changed (see `pkg/cluster/plan/drift.go`). Phase 3
append-merge does not consult the sidecar — it works entirely off the
parsed YAML — but it does refresh it on every run so the next drift
comparison has current facts to compare against. Sidecar permissions
are `0600` since facts include hostnames, NIC addresses, and disk
model strings.

`cluster plan -o` also writes an explicit allowlist sidecar at
`cluster.deploy-disks.json` carrying the *result* of plan's
classification (after applying inventory excludes, ephemeral skip,
and foreign-mount drop). `cluster deploy` reads it and uses it as the
authoritative set of devices `prepareUnmountedDisks` is permitted to
mkfs+mount.

**Fail-closed contract.** Deploy detects plan-generated configs by
a stable `# Generated by seaweed-up plan ...` marker stamped on the
first line of `cluster.yaml`. The marker travels with the file, so
an operator who copies just `cluster.yaml` without its sidecars
still gets fail-closed treatment instead of silent fallback. When
the marker is present, the deploy-disks sidecar is missing or
unparseable, AND this deploy will actually touch disks (the volume
role is included AND `--mount-disks` is on), deploy errors out —
otherwise a lost or truncated sidecar would silently format disks
plan deliberately classified out (excludes, ephemeral, foreign).
Disk-irrelevant deploys (`--component=master`, `--mount-disks=false`)
log a warning and proceed, so plan users can still ship master-only
or service-only updates without lugging the sidecar around.
Hand-written `cluster.yaml` files (no marker, no sidecars) take
the legacy scan-everything path unchanged.

Per-target folder count is also enforced. Before fanning the
volume-server deploys out, `DeployCluster` aggregates each SSH
target's mountpoint demand (`sum of len(folders) + (1 if
dir.idx else 0)` across every `volume_server` whose
`<ip>:<ssh-port>` matches). Each `DeployVolumeServer` then refuses
the host unless the deploy-disks sidecar carries at least the host
total of plan-approved disks. The aggregate matters for
`--volume-server-shape=per-disk`: N one-folder specs on the same
host need N approved disks even though each spec individually only
asks for one. Without the aggregate, a stale one-disk sidecar would
clear each per-disk spec individually and `prepareUnmountedDisks`
(gated to run once per target) would mount only the first disk —
the later `/data<N>` folders would be silently mkdir'd on rootfs.
The check fires before any SSH work; the error names the host
total, the approved count, and tells the operator to re-run plan.

A static count check isn't enough on its own: a sidecar with the
right total can still produce too few mounts at deploy time if one
of the approved devices acquired a partition, was mounted
elsewhere, or disappeared between plan and deploy.
`prepareUnmountedDisks` then silently drops the drifted device from
its candidate set, mounts the others, and the missing `/data<N>`
falls back to a plain rootfs directory. To close that gap, a
runtime mountpoint check runs **after** `prepareUnmountedDisks` and
**before** `ensureVolumeFolders` mkdirs anything: every
`-dir`/`-dir.idx` path the spec lists must be a real mountpoint on
the host. The verification is a single SSH round-trip
(`mountpoint -q` per path); any path that fails is reported back so
the operator sees every drift in one error message. Hand-written
specs (no plan marker) skip both the static and the runtime checks
for backwards compatibility.

The two checks are gated independently. The static count guard
needs the sidecar's allowlist contents and is gated on
`PlannedDisksBySSHTarget != nil`. The runtime mountpoint check
only needs the spec's folder paths and is gated on a separate
`PlanGenerated` flag the cmd layer sets from the marker. This
matters for `--mount-disks=false`: the cmd layer keeps the sidecar
optional in that mode (so plan users can still ship master-only or
service-only updates without lugging the sidecar around), but a
plan-generated cluster.yaml deployed without its sidecar would
otherwise reach `DeployVolumeServer` with both gates disabled and
silently start `weed volume` on rootfs directories. With the
runtime check gated on `PlanGenerated` instead, the sidecar stays
optional but the mount-or-refuse contract holds.

**Deterministic /data<N> assignment.** `prepareUnmountedDisks` walks
its candidate disks in path-sorted order so deploy's `/data<N>`
assignment matches plan's. Without this, Go's randomized map
iteration could mount disk B at `/data1` while the cluster.yaml's
`folders[/data1]` `max` was computed from disk A's size — the
volume server would then run with flags that don't fit the actual
underlying disk.

## Plan: generation

For each inventory host (skipping `external` hosts during probe):

- **master**: append `MasterServerSpec{Ip, Port: 9333, PortSsh: …}`.
- **volume**: append `VolumeServerSpec{Ip, Port: 8080, Folders, IdxFolder: derive(facts.Disks)}`.
  When `defaults.disk.auto_idx_tier` is set on the inventory and a host
  has both rotational and non-rotational eligible disks with an
  unambiguous size gap (smallest fast ≤ `idx_tier_size_ratio` × smallest
  slow; default 1/3), plan carves the smallest **fresh** non-rotational
  disk out of the data tier and routes it to `weed volume -dir.idx=…`.
  Matches the helm chart's `volume.idx` field — small fast SSDs hold
  the per-volume `.idx` files while bulk HDDs absorb the data writes.
  Hosts with uniform tiers (all-fast or all-slow) get no carve-out.
  Cluster-claimed fast disks (already mounted at `/data<N>`) are
  excluded from carve-out: re-routing a previously-deployed data disk
  to `-dir.idx` would orphan whatever volumes are stored there. To
  enable idx tiering on a host that already has a fast disk in service,
  drain it, wipe it, and re-deploy explicitly. The size-gap reference
  for the slow tier still includes claimed disks, so the comparison
  matches the host's actual data tier rather than only its fresh
  fraction.
  - Before classification, the planner drops any disk the probe
    flagged as **ephemeral** (cloud instance store: AWS Nitro
    instance store via the `Amazon EC2 NVMe Instance Storage` MODEL
    string, GCP local SSD via `/dev/disk/by-id/google-local-*`
    symlinks). Skipped disks land in `Report.EphemeralDisksSkipped`
    so the operator sees what was excluded. Set
    `defaults.disk.allow_ephemeral: true` to keep them — useful for
    cache or scratch tiers where the data is intentionally
    disposable.
  - `derive` classifies each remaining disk into one of three buckets
    using *effective mountpoint* = `MountPoint || FstabMountPoint`
    (the kernel's view first, fstab as a fallback for disks that
    haven't been auto-mounted yet on this boot):
      - **Cluster-claimed** (effective mountpoint matches `/data\d+`):
        re-emit the existing folder using its current path. Deploy
        won't mkfs or remount these — they're already ours. Lets a
        re-plan against a deployed cluster reproduce the existing
        `cluster.yaml` rather than silently dropping its folders.
      - **Foreign mount** (any other effective mountpoint, e.g.
        `/`, `/home`, `/var/lib/docker`): skip silently. We never
        reformat a disk we didn't claim.
      - **Fresh** (no effective mountpoint, no `FSType`, not
        excluded by `defaults.disk.exclude`): allocate the next
        free `/data<N>` slot, skipping any N already claimed.
        Deploy will mkfs and mount.
    Disks with a filesystem but no mount and no fstab claim are
    skipped: they were probably formatted by something else, and
    silently reformatting would lose data. The probe parses
    `/etc/fstab` so this classification holds even if `fstab`
    declarations haven't been realized into kernel mounts yet
    (boot race, manual umount, fstab edited but no `mount -a`).
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

Three equivalent ways to supply the DSN, in priority order
(`file > flag > env`, matching the `flag-overrides-env` convention
used by cobra/viper-based CLIs):

```shell
# 1. file (recommended: avoids leaking the password via `ps`)
seaweed-up cluster plan -i inventory.yaml -o cluster.yaml \
    --filer-backend-file /etc/seaweed-up/filer.dsn

# 2. direct CLI flag (overrides the env var for one-off invocations;
#    note the security caveat below)
seaweed-up cluster plan -i inventory.yaml -o cluster.yaml \
    --filer-backend 'postgres://seaweed:CHANGE_ME@10.0.0.41:5432/seaweedfs?sslmode=disable'

# 3. environment variable (good for CI; wins only when neither file
#    nor flag is supplied)
SEAWEEDUP_FILER_BACKEND='postgres://seaweed:s3cret@10.0.0.41/seaweedfs' \
    seaweed-up cluster plan -i inventory.yaml -o cluster.yaml
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
`<ip>:<role's default port>`. Multi-process-per-host is a planner
concern, exposed via `--volume-server-shape`:

- `per-host` (default) — one `volume_server` per host, all eligible
  disks listed under its `folders:`. Single process owns the whole
  data layer for the box.
- `per-disk` — one `volume_server` per eligible disk, with distinct
  ports (`8080`, `8081`, ...). Matches the helm chart's "1 process
  per disk" replicas pattern; gives fault isolation (a single
  volume-process crash doesn't take down sibling disks on the same
  host). The existing `cluster deploy` path supports this without
  changes: each entry gets its own systemd unit (`seaweed_volumeN.service`)
  and per-instance data dir (`<data_dir>/volumeN`), and the per-host
  `prepareUnmountedDisks` mount step is idempotent across the
  multiple SSH calls into the same host. `cluster scale-in` already
  keys on `ip:port` so individual instances can be removed without
  touching siblings.

Future shapes (e.g. `per-rack`, `per-numa-node`) can land as
additional enum values without a new flag.

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
| Role=volume but no free disks found | Drop the volume role for that host (other roles still emit) and report it on stderr via `Report.VolumeHostsNoDisks`. Emitting `folders: []` would silently start `weed volume` against the working directory because `addToBuffer` omits the `-dir` flag when the list is empty. |
| `cluster.yaml` does not yet exist | Generate from scratch. Header comment: `# Generated by seaweed-up plan from inventory.yaml. Safe to hand-edit.` |
| `-o` points to an existing file | Append-merge: parse as `yaml.Node`, append fresh inventory hosts to each `*_servers` section, leave existing entries (and operator hand-edits / comments) byte-stable. Pass `--overwrite` to regenerate from scratch instead. |
| Probed disk is already mounted at `/data<N>` | Skip re-provisioning; emit the folder in the spec using the existing mountpoint. Matches current `prepareUnmountedDisks` skip-on-mount behavior. |
| `plan --dry-run` | Run probe + Marshal/Merge end to end, then print a unified diff of `-o` (current vs proposed) to stdout and exit. Sidecars are summarized but not written; the on-disk file is untouched. Requires `-o` (without it there's no diff target to render). |

## Code layout

```
cmd/
  cluster_plan.go            # unified command. flags: -i, -o, --json,
                             #   --overwrite, --filer-backend,
                             #   --filer-backend-file, --volume-size-limit-mb,
                             #   --volume-server-shape, --cluster-name,
                             #   --concurrency. Phase 1 implements --json only;
                             #   Phase 2 adds -o; Phase 3 adds in-place
                             #   append-merge for an existing -o file.
                             #   Phase 4 adds --dry-run (preview) and will
                             #   add --refresh-host as use cases emerge.

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
3. **Phase 3: plan (append-merge). [Done — PR #69.]** The `yaml.Node`
   merge. Re-running `plan -o cluster.yaml` against an existing file
   parses it as a `yaml.Node` tree, indexes each `*_servers` section by
   `ip:port` (or `ip:<role-sentinel>` for worker/envoy), appends fresh
   inventory hosts that aren't already present, and re-encodes
   preserving every existing byte. `--overwrite` is the escape hatch
   that regenerates from scratch (the Phase 2 behavior). Sidecars
   (`facts.json`, `deploy-disks.json`) are always rewritten from the
   latest probe — they're audit + deploy contracts, not hand-edit
   surfaces. Stability guarantees covered by `pkg/cluster/plan/
   merge_test.go`: no-op byte-identical, append diff isolated to the
   tail, user `max:` edits survive, orphans warn without removal,
   inline comments preserved, hand-written specs (no marker) merge
   without picking up the marker.
4. **Phase 4: ergonomics.** A grab-bag of operator-facing
   improvements rolling out one flag at a time as use uncovers
   what's worth the surface area:
   - **`--dry-run`** *(done)* — print a unified diff of what `-o`
     would change without writing anything. Reuses Marshal/Merge
     end to end and renders via the in-tree LCS-based diff in
     `pkg/cluster/plan/diff.go`. Sidecars are summarized but not
     touched. Pairs naturally with append-merge: operators preview
     before letting plan mutate the file.
   - **Drift detection** *(done)* — load the previous
     `cluster.facts.json` before the fresh probe writes its
     replacement; for each host present on both sides, compare disk
     paths and surface a `WARN: drift on <host>: added /…; removed
     /…` line so operators see hardware that's shifted since the
     last plan run. Currently disk-path-only on purpose: size,
     model, NIC, and CPU signals are too noisy on cloud hosts
     (resizes, NIC reattach) to be worth flagging today; expand one
     dimension at a time if real use surfaces a need. Implementation
     in `pkg/cluster/plan/drift.go`.
   - **`--refresh-host=<ip>`** *(done)* — re-emit a host's entries
     from fresh facts during append-merge while leaving every other
     entry's bytes intact. Repeatable; pairs with the drift WARN
     above (operator sees `drift on 10.0.0.21: added /dev/sdc`,
     re-runs `plan --refresh-host 10.0.0.21` to fix that one
     entry without resorting to `--overwrite`). Refresh is per-IP,
     so a host that maps to multiple sections (master + filer +
     volume) gets all of its entries refreshed in one shot.
     Entry-level head/line/foot comments survive; field-level
     inline comments inside the mapping (e.g. on `port:`) do not —
     pairing each fresh field with its old counterpart by key is
     out of scope for now. Misses (an IP that didn't match any
     existing entry) surface as a `WARN: --refresh-host …` line.
     Implementation extends `pkg/cluster/plan/merge.go`.
   - **Inventory `tag:` references** *(done)* — declare a host as
     `roles: [external]` with `tag: postgres-metadata`, then write
     `--filer-backend postgres://user:pw@tag:postgres-metadata:5432/db`.
     Plan substitutes the tagged host's IP before parsing the DSN,
     so the generated `cluster.yaml` carries the resolved address
     and `cluster deploy` doesn't need to know about tags. Decouples
     "where the metadata DB lives" (inventory edit) from "what its
     credentials are" (DSN file). Tag uniqueness is enforced at
     inventory load; missing-tag references error at plan time
     rather than reaching the DSN parser. v6 IPs get bracketed for
     URL safety. Implementation in `pkg/cluster/plan/tagrewrite.go`.

With this last item Phase 4 is feature-complete. Phases 1–3 are the
minimum product; Phase 4 is ice cream — each flag landed
independently as it earned its keep.

## Resolved design decisions

1. **Role assignment: inventory-only.** No heuristics. Every host
   declares its roles explicitly.
2. **`FolderSpec` is not extended.** No `block_device` / `uuid` fields in
   the YAML. Folder path remains the sole key; UUID is rediscovered at
   deploy time via `blkid` (already the case post-#65).
3. **Filer metadata store: DSN via flag, file, or env var.** When
   absent, `plan` emits a placeholder `# TODO: config:` block per filer.
   When present, the DSN is parsed and expanded into each filer's
   `config:` section. Three input forms in priority order
   `file > flag > env` (matches the cobra/viper convention of
   flag-overrides-env): `--filer-backend-file` (recommended — no
   `ps` leak), direct `--filer-backend` flag (convenient one-off
   override; leaks the DSN via `/proc/<pid>/cmdline`), or
   `SEAWEEDUP_FILER_BACKEND` env var (good for CI).
4. **Merge key is `ip:port` in `cluster.yaml`, but inventory carries
   no service-port field.** The cluster.yaml spec already uses
   `ip:port` and `plan` preserves that. The inventory stays minimal:
   it does not carry a per-host service-port override. `plan` emits
   the role's well-known default (9333, 8888, 8080, …) on synthesis;
   multi-process-per-host volume layouts are reachable via
   `--volume-server-shape=per-disk`, which assigns ports per-disk
   (`8080`, `8081`, …). Keeps inventory host-centric and avoids
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
8. **Admin UI is single-instance; refuse multiples at validation
   time.** SeaweedFS's `weed admin` component has no leader
   election or shared-state story, so two instances would race on
   task scheduling. Both `inventory.Validate()` (plan-side) and
   `Manager.DeployCluster` (deploy-side, for hand-written specs)
   refuse specs with more than one admin entry up front, before
   any SSH session opens. Zero admins is allowed — the cluster
   runs without the admin UI.
9. **Workers run with `-jobType=all` by default.** Stamping the
   default explicitly on every plan-generated `worker_servers`
   entry makes the rendered `cluster.yaml` self-describing —
   operators don't have to know `weed worker`'s implicit default
   to predict task coverage. Override per-pool via
   `worker_servers[].jobType: ec,balance` etc. when sharding task
   handling.
10. **Inbound roles default to `ip.bind: 0.0.0.0`.** SeaweedFS's
    `weed master`/`weed volume`/`weed filer`/`weed s3`/`weed sftp`/
    `weed admin` all bind 127.0.0.1 when `-ip.bind` is unset, which
    makes them unreachable across the network in any multi-host
    deploy. Plan stamps `ip.bind: 0.0.0.0` on every inbound role
    so the generated cluster.yaml works out of the box; operators
    on multi-NIC hosts that need to bind a specific interface
    hand-edit the field, and merge runs preserve the override.
    Hand-written specs that omit `ip.bind:` keep the upstream
    127.0.0.1 default — plan doesn't reach into specs it didn't
    generate.

## Open questions

None blocking Phase 1. Flag as they come up during implementation.
