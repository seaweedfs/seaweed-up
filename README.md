# seaweed-up
Bootstrap SeaweedFS over SSH

## Install
```
$ git checkout https://github.com/seaweedfs/seaweed-up.git
$ cd seaweed-up
$ go install
```

## Usage

### Generate an example template file

```
$ seaweed-up scaffold

# Global variables are applied to all deployments and used as the default values
global:
  # SSH port of servers in the managed cluster.
  port.ssh: 22
  # Storage directory for cluster deployment files, startup scripts, and configuration files.
  dir.conf: "/etc/seaweed"
  # Data directory for cluster metadata files, volume files, and log files.
  dir.data: "/opt/seaweed"
  # Supported values: "amd64", "arm64" (default: "amd64")
  arch: "amd64"
  # volume size limit in MB
  volumeSizeLimitMB: 5000

# Server configs are used to specify the configuration of master servers.
master_servers:
  # The ip address of the master server.
  - ip: 192.168.2.7
    port: 9333

# Server configs are used to specify the configuration of volume servers.
volume_servers:
  # The ip address of the volume server.
  - ip: 192.168.2.7
    port: 8382
    folders:
      - folder: .
        disk: ""

# Server configs are used to specify the configuration of volume servers.
filer_servers:
  # The ip address of the volume server.
  - ip: 192.168.2.7
    port: 8888

```

Save the generated template file, and adjust the content accordingly.

### Deploy the cluster

Assuming the template file is `t.yaml`

```
$ seaweed-up deploy -f t.yaml

```
