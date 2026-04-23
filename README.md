# seaweed-up

Bootstrap and manage SeaweedFS clusters over SSH.

## Install

```bash
git clone https://github.com/seaweedfs/seaweed-up.git
cd seaweed-up
go install
```

## Cluster topology

A typical SeaweedFS deployment has three layers. `seaweed-up` knows how to
deploy and operate all of them from a single YAML file.

| Layer           | Components                | Role                                       |
|-----------------|---------------------------|--------------------------------------------|
| Storage         | `master_servers`          | Raft quorum, volume topology (run 3)       |
|                 | `volume_servers`          | Store data needles (scale for capacity)    |
| File access     | `filer_servers`           | Metadata gateway (run 3, external DB)      |
|                 | `s3_servers`              | S3 API gateway (scale for throughput)      |
|                 | `sftp_servers`, `envoy_servers` | Optional additional frontends        |
| Backend ops     | `admin_servers`           | Coordinate balancing, EC, vacuum (run 1)   |
|                 | `worker_servers`          | Execute admin-scheduled tasks (run a few)  |

The admin server and workers are **not on the data path** — restarting them
does not interrupt reads or writes.

## Configuration examples

Two example specs live under `examples/`:

- [`examples/minimum.yaml`](examples/minimum.yaml) — single-host all-in-one,
  useful for local smoke tests.
- [`examples/typical.yaml`](examples/typical.yaml) — production-shaped
  topology with 3 masters, 3 volume servers, 3 filers (PostgreSQL metadata),
  2 S3 gateways, 1 admin, and 2 workers.

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
`master`, `volume`, `filer`, `s3`, `sftp`, `envoy`, `admin`, `worker`.

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
