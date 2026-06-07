package manager

import (
	"context"
	"fmt"

	"github.com/seaweedfs/seaweed-up/pkg/config"
)

// devArch is the architecture seaweed-up resolves rolling "dev" builds
// for. The dev release datestamps each os/arch independently, so a single
// controller-side resolution targets one arch; amd64 is the SeaweedFS CI
// and the overwhelmingly common deploy target. Multi-arch dev clusters
// would need per-host resolution (follow-up).
const devArch = "amd64"

// resolveDevAsset resolves the newest rolling "dev" build into m.devAsset
// when the target version is "dev". It is a no-op for normal versions and
// is safe to call more than once (re-resolves so a moving dev tag is
// picked up on the next deploy/upgrade). largeDisk is always true:
// install.sh builds the *_large_disk asset, so dev tracks the matching
// weed-large-disk-* build.
func (m *Manager) resolveDevAsset(version string) error {
	if version != config.DevTag {
		m.devAsset = nil
		return nil
	}
	owner, repo := m.ReleaseOwnerRepo()
	asset, err := config.ResolveDevAsset(context.Background(), owner, repo, true, devArch)
	if err != nil {
		return err
	}
	info(fmt.Sprintf("Resolved dev build %s -> %s", asset.BuildID, asset.DownloadURL))
	m.devAsset = &asset
	return nil
}
