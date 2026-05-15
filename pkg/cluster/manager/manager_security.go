package manager

import (
	"context"
	"fmt"
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
	if specification.Name == "" {
		return fmt.Errorf("ensure security.toml: cluster name is required to persist jwt.filer_signing key")
	}
	jwtWrite, err := sutls.LoadOrGenerateFilerSigningKey(specification.Name, "write")
	if err != nil {
		return fmt.Errorf("load/generate filer signing key (write): %w", err)
	}
	jwtRead, err := sutls.LoadOrGenerateFilerSigningKey(specification.Name, "read")
	if err != nil {
		return fmt.Errorf("load/generate filer signing key (read): %w", err)
	}

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
		return fmt.Errorf("ensure security.toml: %d host(s) failed", len(hostErrs))
	}
	return nil
}
