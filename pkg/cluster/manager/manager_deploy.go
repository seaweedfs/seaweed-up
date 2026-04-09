package manager

import (
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"net"
	"strconv"
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

// extraConfigFile describes an additional file to upload into the per-instance
// config dir during deployComponentInstance. Used for things like the S3 IAM
// config (s3.json) that are component-specific.
type extraConfigFile struct {
	// Name is the base name written under <tmp>/config/<Name>.
	Name string
	// Content is the file body.
	Content *bytes.Buffer
	// Mode is the octal mode string passed to Upload (e.g. "0600").
	Mode string
}

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

// resolveWorkerDefaultAdmins returns the default admin endpoint(s) used when
// an individual WorkerServerSpec does not supply its own Admin field.
//
// `weed worker -admin` points to the SeaweedFS admin server (the `weed admin`
// component), NOT a master. The admin component serves port 23646. Workers
// MUST therefore talk to an admin server; defaulting to a master IP would
// silently target the wrong host because masters listen on 9333, not 23646.
//
// Precedence:
//  1. If every worker has its own explicit Admin, no default is needed and
//     the returned slice may be empty (callers still use per-worker Admin).
//  2. Otherwise, if the cluster spec defines at least one admin_server, use
//     the first admin server's ip:port as the default.
//  3. Otherwise, return a clear error rather than silently falling back to a
//     master IP — that would be incorrect.
func resolveWorkerDefaultAdmins(specification *spec.Specification) ([]string, error) {
	var defaultAdmins []string
	if len(specification.AdminServers) > 0 {
		adminSpec := specification.AdminServers[0]
		adminPort := adminSpec.Port
		if adminPort == 0 {
			adminPort = 23646
		}
		defaultAdmins = append(defaultAdmins, fmt.Sprintf("%s:%d", adminSpec.Ip, adminPort))
	}
	for _, workerSpec := range specification.WorkerServers {
		if workerSpec.Admin == "" && len(defaultAdmins) == 0 {
			return nil, fmt.Errorf("worker %s:%d requires an admin endpoint: set worker_servers[].admin or define at least one admin_server", workerSpec.Ip, workerSpec.PortSsh)
		}
	}
	return defaultAdmins, nil
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

	// S3 gateways depend on filers being up, so they are deployed as a
	// separate phase after volume/filer. Use the same errgroup pattern
	// with a mutex-guarded error slice so all failing hosts are surfaced.
	if m.shouldInstall("s3") && len(specification.S3Servers) > 0 {
		var s3eg errgroup.Group
		if m.Concurrency > 0 {
			s3eg.SetLimit(m.Concurrency)
		}
		var (
			s3ErrMu  sync.Mutex
			s3Errors []error
		)
		recordS3Err := func(err error) {
			s3ErrMu.Lock()
			defer s3ErrMu.Unlock()
			fmt.Printf("[ERROR] %v\n", err)
			s3Errors = append(s3Errors, err)
		}
		for index, s3Spec := range specification.S3Servers {
			s3eg.Go(func() error {
				if err := m.DeployS3Server(s3Spec, index); err != nil {
					wrapped := fmt.Errorf("deploy s3 server %s:%d: %w", s3Spec.Ip, s3Spec.PortSsh, err)
					recordS3Err(wrapped)
				}
				return nil
			})
		}
		_ = s3eg.Wait()
		if len(s3Errors) > 0 {
			if len(s3Errors) == 1 {
				return s3Errors[0]
			}
			return fmt.Errorf("%d deploy errors: %w", len(s3Errors), stderrors.Join(s3Errors...))
		}
	}

	// Admin servers depend on masters being up, so they are deployed as a
	// separate phase after volume/filer. Use the same errgroup pattern
	// with a mutex-guarded error slice so all failing hosts are surfaced.
	if m.shouldInstall("admin") && len(specification.AdminServers) > 0 {
		var adminEg errgroup.Group
		if m.Concurrency > 0 {
			adminEg.SetLimit(m.Concurrency)
		}
		var (
			adminErrMu  sync.Mutex
			adminErrors []error
		)
		recordAdminErr := func(err error) {
			adminErrMu.Lock()
			defer adminErrMu.Unlock()
			fmt.Printf("[ERROR] %v\n", err)
			adminErrors = append(adminErrors, err)
		}
		for index, adminSpec := range specification.AdminServers {
			adminEg.Go(func() error {
				if err := m.DeployAdminServer(masters, adminSpec, index); err != nil {
					wrapped := fmt.Errorf("deploy admin server %s:%d: %w", adminSpec.Ip, adminSpec.PortSsh, err)
					recordAdminErr(wrapped)
				}
				return nil
			})
		}
		_ = adminEg.Wait()
		if len(adminErrors) > 0 {
			if len(adminErrors) == 1 {
				return adminErrors[0]
			}
			return fmt.Errorf("%d deploy errors: %w", len(adminErrors), stderrors.Join(adminErrors...))
		}
	}

	if m.shouldInstall("worker") && len(specification.WorkerServers) > 0 {
		// Resolve the default admin endpoint used when a worker spec does not
		// supply one of its own. See resolveWorkerDefaultAdmins for the
		// precedence rules.
		defaultAdmins, err := resolveWorkerDefaultAdmins(specification)
		if err != nil {
			return err
		}

		// Fan out worker deploys using the same errgroup + shared error
		// slice pattern as the volume/filer phase above so that every
		// failing host is surfaced to the caller.
		var workerEg errgroup.Group
		if m.Concurrency > 0 {
			workerEg.SetLimit(m.Concurrency)
		}
		var (
			workerErrMu  sync.Mutex
			workerErrors []error
		)
		recordWorkerErr := func(err error) {
			workerErrMu.Lock()
			defer workerErrMu.Unlock()
			fmt.Printf("[ERROR] %v\n", err)
			workerErrors = append(workerErrors, err)
		}
		for index, workerSpec := range specification.WorkerServers {
			workerEg.Go(func() error {
				if err := m.DeployWorkerServer(defaultAdmins, workerSpec, index); err != nil {
					wrapped := fmt.Errorf("deploy worker server %s:%d: %w", workerSpec.Ip, workerSpec.PortSsh, err)
					recordWorkerErr(wrapped)
				}
				return nil
			})
		}
		_ = workerEg.Wait()
		if len(workerErrors) > 0 {
			if len(workerErrors) == 1 {
				return workerErrors[0]
			}
			return fmt.Errorf("%d deploy errors: %w", len(workerErrors), stderrors.Join(workerErrors...))
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
		defaultFiler = net.JoinHostPort(f.Ip, strconv.Itoa(port))
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
	for _, adminSpec := range specification.AdminServers {
		adminSpec.PortSsh = utils.NvlInt(adminSpec.PortSsh, m.SshPort, 22)
	}
	for _, workerSpec := range specification.WorkerServers {
		workerSpec.PortSsh = utils.NvlInt(workerSpec.PortSsh, m.SshPort, 22)
	}
}

func (m *Manager) deployComponentInstance(op operator.CommandOperator, component string, componentInstance string, cliOptions *bytes.Buffer, extras ...extraConfigFile) error {
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

	for _, extra := range extras {
		if extra.Content == nil {
			continue
		}
		mode := extra.Mode
		if mode == "" {
			mode = "0644"
		}
		if err := op.Upload(extra.Content, fmt.Sprintf("%s/config/%s", dir, extra.Name), mode); err != nil {
			return fmt.Errorf("error received during upload %s: %w", extra.Name, err)
		}
	}

	info("Installing " + componentInstance + "...")
	err = op.Execute(fmt.Sprintf("cat %s/install_%s.sh | SUDO_PASS=%s sh -\n", dir, componentInstance, shellSingleQuote(m.sudoPass)))
	if err != nil {
		return fmt.Errorf("error received during installation: %s", err)
	}

	info("Done.")
	return nil
}
