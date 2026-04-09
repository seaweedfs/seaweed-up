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
	"golang.org/x/sync/errgroup"
)

func (m *Manager) shouldInstall(c string) bool {
	return m.ComponentToDeploy == "" || m.ComponentToDeploy == c
}

func (m *Manager) DeployCluster(specification *spec.Specification) error {
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

	// Fan out volume and filer server deploys using errgroup.
	//
	// Note: errgroup.Wait() only returns the first non-nil error. To ensure
	// that operators see ALL per-host failures (not just the first), we
	// collect every error into a mutex-guarded slice, log each of them, and
	// build a combined error message that mentions every failing host.
	var eg errgroup.Group
	if m.Concurrency > 0 {
		eg.SetLimit(m.Concurrency)
	}

	var (
		errMu        sync.Mutex
		deployErrors []error
	)
	recordErr := func(err error) {
		errMu.Lock()
		defer errMu.Unlock()
		fmt.Printf("[ERROR] %v\n", err)
		deployErrors = append(deployErrors, err)
	}

	if m.shouldInstall("volume") {
		for index, volumeSpec := range specification.VolumeServers {
			eg.Go(func() error {
				if err := m.DeployVolumeServer(masters, volumeSpec, index); err != nil {
					wrapped := fmt.Errorf("deploy volume server %s:%d: %w", volumeSpec.Ip, volumeSpec.PortSsh, err)
					recordErr(wrapped)
				}
				return nil
			})
		}
	}
	if m.shouldInstall("filer") {
		for index, filerSpec := range specification.FilerServers {
			eg.Go(func() error {
				if err := m.DeployFilerServer(masters, filerSpec, index); err != nil {
					wrapped := fmt.Errorf("deploy filer server %s:%d: %w", filerSpec.Ip, filerSpec.PortSsh, err)
					recordErr(wrapped)
				}
				return nil
			})
		}
	}
	// Wait for all goroutines to complete. We intentionally ignore
	// eg.Wait()'s single-error return and build a combined error from the
	// full slice so that every failing host is surfaced to the caller.
	_ = eg.Wait()
	if len(deployErrors) > 0 {
		if len(deployErrors) == 1 {
			return deployErrors[0]
		}
		return fmt.Errorf("%d deploy errors: %w", len(deployErrors), stderrors.Join(deployErrors...))
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
