package manager

import (
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"sync"

	"github.com/pkg/errors"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/config"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
	"github.com/seaweedfs/seaweed-up/pkg/utils"
	"github.com/seaweedfs/seaweed-up/scripts"
	"github.com/thanhpk/randstr"
)

func (m *Manager) shouldInstall(c string) bool {
	return m.ComponentToDeploy == "" || m.ComponentToDeploy == c
}

// validateS3Prerequisites ensures that any S3 gateway in the spec has a filer
// it can talk to — either via an explicit `filer:` on the S3 entry, or via a
// filer_servers section that the deploy logic can default to.
func validateS3Prerequisites(specification *spec.Specification) error {
	if len(specification.S3Servers) == 0 {
		return nil
	}
	if len(specification.FilerServers) > 0 {
		return nil
	}
	for _, s3 := range specification.S3Servers {
		if s3.Filer == "" {
			return fmt.Errorf("invalid cluster spec: S3 gateway requires a filer; define filer_servers or set `filer:` on each s3_servers entry")
		}
	}
	return nil
}

func (m *Manager) DeployCluster(specification *spec.Specification) error {
	if err := validateS3Prerequisites(specification); err != nil {
		return err
	}
	m.prepare(specification)

	var masters []string
	for _, masterSpec := range specification.MasterServers {
		masters = append(masters, fmt.Sprintf("%s:%d", masterSpec.Ip, masterSpec.Port))
	}

	if m.shouldInstall("master") {
		for index, masterSpec := range specification.MasterServers {
			if err := m.DeployMasterServer(masters, masterSpec, index); err != nil {
				fmt.Printf("error is %v\n", err)
				return fmt.Errorf("deploy to master server %s:%d :%v", masterSpec.Ip, masterSpec.PortSsh, err)
			}
		}
	}

	var wg sync.WaitGroup
	var deployErrMu sync.Mutex
	var deployErrors []error

	if m.shouldInstall("volume") {
		for index, volumeSpec := range specification.VolumeServers {
			wg.Add(1)
			go func(index int, volumeSpec *spec.VolumeServerSpec) {
				defer wg.Done()
				if err := m.DeployVolumeServer(masters, volumeSpec, index); err != nil {
					deployErrMu.Lock()
					deployErrors = append(deployErrors, fmt.Errorf("deploy to volume server %s:%d :%w", volumeSpec.Ip, volumeSpec.PortSsh, err))
					deployErrMu.Unlock()
				}
			}(index, volumeSpec)
		}
	}
	if m.shouldInstall("filer") {
		for index, filerSpec := range specification.FilerServers {
			wg.Add(1)
			go func(index int, filerSpec *spec.FilerServerSpec) {
				defer wg.Done()
				if err := m.DeployFilerServer(masters, filerSpec, index); err != nil {
					deployErrMu.Lock()
					deployErrors = append(deployErrors, fmt.Errorf("deploy to filer server %s:%d :%w", filerSpec.Ip, filerSpec.PortSsh, err))
					deployErrMu.Unlock()
				}
			}(index, filerSpec)
		}
	}
	wg.Wait()
	if err := stderrors.Join(deployErrors...); err != nil {
		return err
	}

	if m.shouldInstall("s3") {
		var s3wg sync.WaitGroup
		var s3ErrMu sync.Mutex
		var s3Errors []error
		for index, s3Spec := range specification.S3Servers {
			s3wg.Add(1)
			go func(index int, s3Spec *spec.S3ServerSpec) {
				defer s3wg.Done()
				if err := m.DeployS3Server(s3Spec, index); err != nil {
					s3ErrMu.Lock()
					s3Errors = append(s3Errors, fmt.Errorf("deploy to s3 server %s:%d :%w", s3Spec.Ip, s3Spec.PortSsh, err))
					s3ErrMu.Unlock()
				}
			}(index, s3Spec)
		}
		s3wg.Wait()
		if err := stderrors.Join(s3Errors...); err != nil {
			return err
		}
	}

	if m.shouldInstall("envoy") && len(specification.EnvoyServers) > 0 {
		latest, err := config.GitHubLatestRelease(context.Background(), "0", "envoyproxy", "envoy")
		if err != nil {
			return errors.Wrapf(err, "unable to get latest version number, define a version manually with the --version flag")
		}
		for index, envoySpec := range specification.EnvoyServers {
			envoySpec.Version = utils.Nvl(envoySpec.Version, latest.Version)
			if err := m.DeployEnvoyServer(specification.FilerServers, envoySpec, index); err != nil {
				return fmt.Errorf("deploy to envoy server %s:%d :%v", envoySpec.Ip, envoySpec.PortSsh, err)
			}
		}
	}
	return nil
}

func (m *Manager) prepare(specification *spec.Specification) {
	if m.User != "root" {
		password := utils.PromptForPassword("Input sudo password: ")
		m.sudoPass = password
	}
	m.confDir = utils.Nvl(specification.GlobalOptions.ConfigDir, "/etc/seaweed")
	m.dataDir = utils.Nvl(specification.GlobalOptions.DataDir, "/opt/seaweed")
	for _, masterSpec := range specification.MasterServers {
		masterSpec.VolumeSizeLimitMB = utils.NvlInt(masterSpec.VolumeSizeLimitMB, specification.GlobalOptions.VolumeSizeLimitMB, 5000)
		masterSpec.DefaultReplication = utils.Nvl(masterSpec.DefaultReplication, specification.GlobalOptions.Replication, "")
		masterSpec.PortSsh = utils.NvlInt(masterSpec.PortSsh, m.SshPort, 22)
	}
	for _, volumeSpec := range specification.VolumeServers {
		volumeSpec.PortSsh = utils.NvlInt(volumeSpec.PortSsh, m.SshPort, 22)
	}
	for _, filerSpec := range specification.FilerServers {
		filerSpec.PortSsh = utils.NvlInt(filerSpec.PortSsh, m.SshPort, 22)
	}
	// Default S3 gateways: ssh port from global, filer endpoint from the first filer if unset.
	var defaultFiler string
	if len(specification.FilerServers) > 0 {
		f := specification.FilerServers[0]
		port := f.Port
		if port == 0 {
			port = 8888
		}
		defaultFiler = fmt.Sprintf("%s:%d", f.Ip, port)
	}
	for _, s3Spec := range specification.S3Servers {
		s3Spec.PortSsh = utils.NvlInt(s3Spec.PortSsh, m.SshPort, 22)
		if s3Spec.Filer == "" {
			s3Spec.Filer = defaultFiler
		}
		if s3Spec.Port == 0 {
			s3Spec.Port = 8333
		}
	}
	for _, envoySpec := range specification.EnvoyServers {
		envoySpec.PortSsh = utils.NvlInt(envoySpec.PortSsh, m.SshPort, 22)
	}
}

func (m *Manager) deployComponentInstance(op operator.CommandOperator, component string, componentInstance string, cliOptions *bytes.Buffer) error {
	info("Deploying " + componentInstance + "...")

	dir := "/tmp/seaweed-up." + randstr.String(6)

	defer func() { _ = op.Execute("rm -rf " + dir) }()

	err := op.Execute("mkdir -p " + dir + "/config")
	if err != nil {
		return fmt.Errorf("error received during installation: %s", err)
	}

	data := map[string]interface{}{
		"Component":         component,
		"ComponentInstance": componentInstance,
		"ConfigDir":         m.confDir,
		"DataDir":           m.dataDir,
		"TmpDir":            dir,
		"SkipEnable":        m.skipEnable,
		"SkipStart":         m.skipStart,
		"ForceRestart":      m.ForceRestart,
		"Version":           m.Version,
		"ProxyConfig":       "",
	}

	// Configure proxy if specified
	if m.ProxyUrl != "" {
		data["ProxyConfig"] = "--proxy " + m.ProxyUrl
	}

	installScript, err := scripts.RenderScript("install.sh", data)
	if err != nil {
		return err
	}

	err = op.Upload(installScript, fmt.Sprintf("%s/install_%s.sh", dir, componentInstance), "0755")
	if err != nil {
		return fmt.Errorf("error received during upload install script: %s", err)
	}

	err = op.Upload(cliOptions, fmt.Sprintf("%s/config/%s.options", dir, component), "0644")
	if err != nil {
		return fmt.Errorf("error received during upload %s.options: %s", component, err)
	}

	info("Installing " + componentInstance + "...")
	err = op.Execute(fmt.Sprintf("cat %s/install_%s.sh | SUDO_PASS=\"%s\" sh -\n", dir, componentInstance, m.sudoPass))
	if err != nil {
		return fmt.Errorf("error received during installation: %s", err)
	}

	info("Done.")
	return nil
}
