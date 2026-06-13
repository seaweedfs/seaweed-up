package manager

import (
	"context"
	"fmt"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/config"
)

// specHasRustVolume reports whether any volume server in the spec runs the
// standalone Rust weed-volume binary (which ships as a separate dev asset).
func specHasRustVolume(s *spec.Specification) bool {
	if s == nil {
		return false
	}
	for _, v := range s.VolumeServers {
		if v != nil && v.IsRust() {
			return true
		}
	}
	return false
}

// devArch is the architecture seaweed-up resolves rolling "dev" builds
// for. The dev release datestamps each os/arch independently, so a single
// controller-side resolution targets one arch; amd64 is the SeaweedFS CI
// and the overwhelmingly common deploy target. Multi-arch dev clusters
// would need per-host resolution (follow-up).
const devArch = "amd64"

// resolveDevAsset resolves the newest rolling "dev" build into m.devAsset
// (the Go `weed` server) and, when the cluster has any Rust volume server,
// m.rustDevAsset (the standalone `weed-volume`). It is a no-op for normal
// versions and is safe to call more than once (re-resolves so a moving dev
// tag is picked up on the next deploy/upgrade). largeDisk is always true:
// install.sh builds the *-large-disk asset, so dev tracks the matching
// large-disk build. The two assets are datestamped independently in the dev
// release, so each is resolved on its own.
func (m *Manager) resolveDevAsset(version string, specification *spec.Specification) error {
	if version != config.DevTag {
		m.devAsset = nil
		m.rustDevAsset = nil
		return nil
	}
	owner, repo := m.ReleaseOwnerRepo()
	asset, err := config.ResolveDevAsset(context.Background(), owner, repo, "weed", true, devArch)
	if err != nil {
		return err
	}
	m.info(fmt.Sprintf("Resolved dev build %s -> %s", asset.BuildID, asset.DownloadURL))
	m.devAsset = &asset

	// The Rust volume binary ships as a separate weed-volume-large-disk-*
	// dev asset; resolve it only when a volume server actually runs Rust.
	if specHasRustVolume(specification) {
		rustAsset, err := config.ResolveDevAsset(context.Background(), owner, repo, "weed-volume", true, devArch)
		if err != nil {
			return err
		}
		m.info(fmt.Sprintf("Resolved rust dev build %s -> %s", rustAsset.BuildID, rustAsset.DownloadURL))
		m.rustDevAsset = &rustAsset
	} else {
		m.rustDevAsset = nil
	}
	return nil
}
