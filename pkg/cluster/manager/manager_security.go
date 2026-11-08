package manager

import (
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"strings"
	"sync"

	"github.com/fatih/color"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	sutls "github.com/seaweedfs/seaweed-up/pkg/cluster/tls"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
	"golang.org/x/sync/errgroup"
)

// EnsureSecurityToml writes /etc/seaweed/security.toml with the cluster's
// jwt.filer_signing keys to every filer + admin host. Required even when
// TLS is off so the filer registers the IAM gRPC service the Admin UI
// Users tab calls; the admin server signs Bearer tokens with the same key.
//
// When TLS is enabled, cluster cert init's UploadBundle already writes
// security.toml with both [jwt.filer_signing*] and [grpc.*] sections;
// this method is a no-op in that case to avoid overwriting it.
func (m *Manager) EnsureSecurityToml(specification *spec.Specification) error {
	if specification.GlobalOptions.TLSEnabled {
		return nil
	}
	hosts := sutls.FilerAndAdminHosts(specification)
	if len(hosts) == 0 {
		return nil
	}
	keys, err := loadFilerSigningKeys(specification.Name)
	if err != nil {
		return fmt.Errorf("ensure security.toml: %w", err)
	}
	jwtWrite, jwtRead := keys.write, keys.read

	color.Cyan("Installing security.toml on %d filer/admin host(s) for IAM gRPC", len(hosts))

	var (
		mu       sync.Mutex
		hostErrs []error
	)
	g, _ := errgroup.WithContext(context.Background())
	if m.Concurrency > 0 {
		g.SetLimit(m.Concurrency)
	}
	for _, h := range hosts {
		h := h
		g.Go(func() error {
			port := h.SSHPort
			if port == 0 {
				port = m.SshPort
			}
			if port == 0 {
				port = 22
			}
			address := fmt.Sprintf("%s:%d", h.IP, port)
			color.Yellow("  -> %s (%s)", h.IP, h.Role)
			err := operator.ExecuteRemote(address, m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {
				return sutls.UploadSecurityTOMLOnly(op, h.Role, jwtWrite, jwtRead, m.User, m.sudoPass)
			})
			if err != nil {
				mu.Lock()
				hostErrs = append(hostErrs, fmt.Errorf("install security.toml on %s: %w", h.IP, err))
				mu.Unlock()
			}
			return nil
		})
	}
	_ = g.Wait()
	if len(hostErrs) > 0 {
		if len(hostErrs) == 1 {
			return hostErrs[0]
		}
		return fmt.Errorf("ensure security.toml: %d host(s) failed: %w", len(hostErrs), stderrors.Join(hostErrs...))
	}
	return nil
}

// filerSigningKeys are the cluster's persisted jwt.filer_signing keys.
type filerSigningKeys struct {
	write, read string
}

func loadFilerSigningKeys(clusterName string) (filerSigningKeys, error) {
	if clusterName == "" {
		return filerSigningKeys{}, fmt.Errorf("cluster name is required to persist jwt.filer_signing key")
	}
	write, err := sutls.LoadOrGenerateFilerSigningKey(clusterName, "write")
	if err != nil {
		return filerSigningKeys{}, fmt.Errorf("load/generate filer signing key (write): %w", err)
	}
	read, err := sutls.LoadOrGenerateFilerSigningKey(clusterName, "read")
	if err != nil {
		return filerSigningKeys{}, fmt.Errorf("load/generate filer signing key (read): %w", err)
	}
	return filerSigningKeys{write: write, read: read}, nil
}

// installsAdminOrWorker reports whether a deploy installs admin or worker
// instances.
func (m *Manager) installsAdminOrWorker(specification *spec.Specification) bool {
	installsAdmin := m.shouldInstall("admin") && len(specification.AdminServers) > 0
	installsWorker := m.shouldInstall("worker") && len(specification.WorkerServers) > 0
	return installsAdmin || installsWorker
}

// loadInstanceSecurity loads the signing keys for admin and worker instances.
// The admin authenticates worker streams with jwt.filer_signing.key, so each
// of those instances gets its own copy of the key.
func (m *Manager) loadInstanceSecurity(specification *spec.Specification) error {
	keys, err := loadFilerSigningKeys(specification.Name)
	if err != nil {
		return fmt.Errorf("admin/worker security.toml: %w", err)
	}
	m.filerSigning = keys
	return nil
}

// instanceSecurityToml is the JWT-only security.toml installed into an
// instance's config dir. /etc/seaweed/security.toml is not on the search path
// of a process started with -config_dir=<instance>.d.
func (m *Manager) instanceSecurityToml(component string) []extraConfigFile {
	if m.filerSigning.write == "" {
		return nil
	}
	toml := sutls.RenderSecurityTOML(component, m.filerSigning.write, m.filerSigning.read, false)
	return []extraConfigFile{{Name: "security.toml", Content: bytes.NewBufferString(toml), Mode: "0600"}}
}

// lanceWorkerEnv is weed-worker's environment file. WEED_MASTERS names the
// masters whose filers it keeps Lance tables through, required after 4.52;
// WEED_FILER_SIGNING_KEY signs its admin stream and its Lance namespace
// calls. Variables, not flags: a release that predates one ignores it.
func (m *Manager) lanceWorkerEnv(masters string) []extraConfigFile {
	var env strings.Builder
	if masters != "" {
		env.WriteString("WEED_MASTERS=" + masters + "\n")
	}
	if m.filerSigning.write != "" {
		env.WriteString("WEED_FILER_SIGNING_KEY=" + m.filerSigning.write + "\n")
	}
	if env.Len() == 0 {
		return nil
	}
	return []extraConfigFile{{Name: "security.env", Content: bytes.NewBufferString(env.String()), Mode: "0600"}}
}
