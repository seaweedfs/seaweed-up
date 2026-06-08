# seaweed-up

Bootstrap and manage SeaweedFS clusters over SSH.

## Install

```bash
git clone https://github.com/seaweedfs/seaweed-up.git
cd seaweed-up
go install
```

## Cluster topology

A typical SeaweedFS deployment has three layers, plus an optional monitoring
stack. `seaweed-up` knows how to deploy and operate all of them from a single
YAML file.

| Layer           | Components                | Role                                       |
|-----------------|---------------------------|--------------------------------------------|
| Storage         | `master_servers`          | Raft quorum, volume topology (run 3)       |
|                 | `volume_servers`          | Store data needles (scale for capacity)    |
| File access     | `filer_servers`           | Metadata gateway (run 3, external DB)      |
|                 | `s3_servers`              | S3 API gateway (scale for throughput)      |
|                 | `sftp_servers`, `envoy_servers` | Optional additional frontends        |
| Backend ops     | `admin_servers`           | Coordinate balancing, EC, vacuum (run 1)   |
|                 | `worker_servers`          | Execute admin-scheduled tasks (run a few)  |
| Observability   | `monitoring`              | node_exporter + Prometheus + Grafana       |

The admin server, workers, and monitoring stack are **not on the data path** —
restarting them does not interrupt reads or writes. Monitoring is optional; see
[Monitoring](#monitoring-prometheus--grafana) below.

## Configuration examples

Two example specs live under `examples/`:

- [`examples/minimum.yaml`](examples/minimum.yaml) — single-host all-in-one,
  useful for local smoke tests.
- [`examples/typical.yaml`](examples/typical.yaml) — production-shaped
  topology with 3 masters, 3 volume servers, 3 filers (PostgreSQL metadata),
  2 S3 gateways, 1 admin, 2 workers, and a co-located Prometheus + Grafana
  monitoring stack.

## Shared config defaults

Cluster-wide values are written once and inherited by each entry, so you
don't repeat them per server:

- **`global.s3_config`** — the s3.json (IAM identities) every `s3_servers`
  entry uses. Define it once under `global`; the secret lives in one place.
- **`s3_servers[].filer`** — defaults to the first `filer_servers` entry.
- **`worker_servers[].admin`** — defaults to the first `admin_servers` entry.

So a multi-gateway / multi-worker spec is just a list of IPs:

```yaml
global:
  s3_config:
    identities:
      - name: app
        credentials: [{ accessKey: APP_KEY, secretKey: CHANGE_ME }]
        actions: [Read, Write, List, Tagging]

s3_servers:        # filer + s3_config inherited
  - { ip: 10.0.0.51 }
  - { ip: 10.0.0.52 }

worker_servers:    # admin inherited from admin_servers[0]
  - { ip: 10.0.0.71 }
  - { ip: 10.0.0.72 }
```

Set any of these per entry to override the default (e.g. point a gateway at
a co-located filer, or a worker at a different admin).

## Deploy

```bash
seaweed-up cluster deploy -f examples/typical.yaml
```

Deploy only a specific component:

```bash
seaweed-up cluster deploy -f cluster.yaml --component=admin
seaweed-up cluster deploy -f cluster.yaml --component=worker
```

Supported `--component` values:
`master`, `volume`, `filer`, `s3`, `sftp`, `envoy`, `admin`, `worker`, `monitoring`.

## Bastion / jump host

When the cluster nodes only have private addresses reachable through a
public jump host (the classic `ssh bastion` then `ssh 10.0.0.x` pattern),
declare the bastion under `global.bastion`. Every connection — deploy,
preflight, lifecycle, upgrade, prepare — is tunnelled through it, so you
can run `seaweed-up` from your laptop:

```yaml
global:
  bastion:
    host: 203.0.113.10      # public jump host; "host" or "host:port"
    user: chris             # optional, defaults to the current OS user
    identity: ~/.ssh/id_rsa # optional, falls back to the ssh agent
    port: 22                # optional, defaults to 22
```

The `master_servers` / `volume_servers` IPs are then the private addresses
as seen *from the bastion*. The `--user` / `--identity` flags still apply
to the nodes themselves; the `bastion:` block holds the jump host's own
credentials. Omit the block for direct connections.

## SSH host-key verification

By default seaweed-up does not verify SSH host keys. To enforce a real
trust boundary (against a man-in-the-middle on the path to the bastion or
the nodes), set `global.ssh_host_key_check`:

```yaml
global:
  ssh_host_key_check: accept-new   # ignore (default) | accept-new | strict
```

## Rust volume server

A volume server can run the standalone Rust `weed-volume` binary instead of
the Go `weed volume`. Both register with the same masters, read the same
options file, and share the on-disk format, so they coexist in one cluster —
set `engine` per volume server:

```yaml
volume_servers:
  - ip: 10.0.0.21          # Go weed volume (default)
  - ip: 10.0.0.22
    engine: rust           # standalone weed-volume binary
```

`engine` accepts `go`/`weed` (default) or `rust`/`weed-volume`. For the `rust`
engine, seaweed-up downloads `weed-volume_large_disk_linux_<arch>.tar.gz` (plus
`.md5`) from the release for `--version` — the asset published by SeaweedFS's
`rust_binaries_release.yml`. With `--enterprise` it pulls
`weed-volume-enterprise_large_disk_linux_<arch>.tar.gz` from the artifactory
release instead, so the rust engine works for both editions.

- `ignore` (default) — no verification; preserves historical behavior.
- `accept-new` — trust-on-first-use: unknown hosts are learned and
  appended to `~/.ssh/known_hosts`, but a host whose key has *changed* is
  rejected. Good for first deploys.
- `strict` — every host (bastion and nodes) must already be in
  `~/.ssh/known_hosts`; anything else is refused.

The policy applies to both the direct node connections and the bastion hop.

## Monitoring (Prometheus + Grafana)

Declare a `monitoring:` block and `cluster deploy` will stand up the full
observability stack as part of the cluster: node_exporter on every master/volume/filer host,
Prometheus and Grafana on the monitoring host, the SeaweedFS metrics ports
auto-enabled on master/volume/filer, and the bundled SeaweedFS dashboard
pre-loaded against a provisioned Prometheus datasource.

```yaml
monitoring:
  host: 10.0.0.1               # runs Prometheus + Grafana
  bind: 127.0.0.1             # localhost by default — reach Grafana via SSH tunnel
  grafana_admin_user: admin
  grafana_admin_password: CHANGE_ME
  # prometheus_port: 9090     # optional
  # grafana_port: 3000        # optional
  # retention: 15d            # optional Prometheus retention
  # node_exporter: true       # optional (default true)
```

```bash
seaweed-up cluster deploy -f cluster.yaml                 # whole cluster + monitoring
seaweed-up cluster deploy -f cluster.yaml --component=monitoring   # just the stack
```

With `bind: 127.0.0.1` (the default) Grafana isn't exposed publicly — reach
it over a tunnel:

```bash
ssh -L 3000:localhost:3000 chris@<monitoring-host>   # then open http://localhost:3000
```

(Grafana binds to `127.0.0.1` on the monitoring host, so the tunnel must
terminate there; add `-J chris@<bastion>` if the host is only reachable
through a jump host.)

Monitoring participates in the lifecycle commands too:

```bash
seaweed-up cluster restart -f cluster.yaml --component=monitoring
```

The lower-level building blocks remain available if you run your own
Prometheus/Grafana: `cluster prometheus-config`, `cluster node-exporter
install`, and `cluster dashboard install`.

Metrics ports are assigned automatically when monitoring is enabled: each
master/volume/filer gets one (unique per host, starting at `9324`), `weed`
is started with `-metricsPort` so it serves `/metrics`, and the scrape config
points at the same ports. To pin a specific port — e.g. for fixed firewall
rules — set `metrics_port:` on a `master_servers` / `volume_servers` /
`filer_servers` entry; explicit values are kept as-is.

## Lifecycle

Start, stop, or restart every service in the cluster, or scope the operation
to a single component:

```bash
seaweed-up cluster start   -f cluster.yaml
seaweed-up cluster stop    -f cluster.yaml --component=worker
seaweed-up cluster restart -f cluster.yaml --component=admin
```

## Other operations

```bash
# Prepare hosts (ulimits, sysctls, firewall, time sync)
seaweed-up cluster prepare -f cluster.yaml

# Check cluster status
seaweed-up cluster status my-cluster

# Rolling upgrade
seaweed-up cluster upgrade my-cluster -f cluster.yaml --version=latest

# Scale out / in
seaweed-up cluster scale out my-cluster --add-volume=2
seaweed-up cluster scale in  my-cluster --remove-node=10.0.0.23

# List and destroy
seaweed-up cluster list
seaweed-up cluster destroy my-cluster --remove-data
```

## Erasure coding

With enough volume servers, enable erasure coding for better durability at
lower storage overhead. For example, with 8 volume servers you can run EC
5+3 (5 data + 3 parity shards) and tolerate up to 3 volume server failures
at 1.6x overhead, versus 3x for full replication.
