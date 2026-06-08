package scripts

import (
	"io"
	"strings"
	"testing"
)

func renderInstall(t *testing.T, data map[string]interface{}) string {
	t.Helper()
	// Fields install.sh always references; tests override as needed.
	base := map[string]interface{}{
		"Component": "master", "ComponentInstance": "master0",
		"ConfigDir": "/etc/seaweed", "DataDir": "/opt/seaweed", "TmpDir": "/tmp/x",
		"SkipEnable": false, "SkipStart": false, "ForceRestart": false,
		"Version": "4.31", "ProxyConfig": "", "ReleaseOwner": "seaweedfs",
		"ReleaseRepo": "seaweedfs", "AssetPrefix": "", "FullSuffix": "_full",
		"DevAssetURL": "", "DevMd5URL": "", "DevBuildID": "",
		"Binary": "weed", "RustVolume": false, "Enterprise": false,
	}
	for k, v := range data {
		base[k] = v
	}
	r, err := RenderScript("install.sh", base)
	if err != nil {
		t.Fatalf("RenderScript: %v", err)
	}
	b, _ := io.ReadAll(r)
	return string(b)
}

func TestInstallScript_DevPath(t *testing.T) {
	out := renderInstall(t, map[string]interface{}{
		"Version":     "dev",
		"DevAssetURL": "https://github.com/seaweedfs/seaweedfs/releases/download/dev/weed-large-disk-20260607-1918-linux-amd64.tar.gz",
		"DevMd5URL":   "https://github.com/seaweedfs/seaweedfs/releases/download/dev/weed-large-disk-20260607-1918-linux-amd64.tar.gz.md5",
		"DevBuildID":  "20260607-1918",
	})
	for _, want := range []string{
		`WANT_ID="20260607-1918"`,
		".weed-dev-buildid",
		"weed-large-disk-20260607-1918-linux-amd64.tar.gz",
		`[ "$curID" = "$WANT_ID" ]`, // content-based skip on the build id
		"md5sum -c",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dev install script missing %q", want)
		}
	}
	// must not fall into the versioned download path
	if strings.Contains(out, "Downloading ${SEAWEED_VERSION} ${assetFileName}") {
		t.Errorf("dev path should bypass the versioned download branch")
	}
}

func TestInstallScript_RustVolumePath(t *testing.T) {
	out := renderInstall(t, map[string]interface{}{
		"Component": "volume", "ComponentInstance": "volume0",
		"Binary": "weed-volume", "RustVolume": true, "Version": "4.31",
	})
	for _, want := range []string{
		"BINARY=weed-volume",
		"weed-volume_large_disk_${OS}_${SUFFIX}.tar.gz",             // versioned per-arch release asset
		".weed-volume-version",                                       // version marker
		"ExecStart=${BIN_DIR}/${BINARY} -options=",                   // no `weed volume` subcommand / go globals
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rust volume install script missing %q", want)
		}
	}
	// must not take the Go versioned/full-asset path or the go-style ExecStart
	if strings.Contains(out, "_full_large_disk.tar.gz") {
		t.Errorf("rust path should not download the Go weed tarball")
	}
	if strings.Contains(out, "-logdir=") || strings.Contains(out, "${COMPONENT} -options=") {
		t.Errorf("rust ExecStart should not use go-style flags / subcommand")
	}
}

func TestInstallScript_RustVolumeEnterprisePath(t *testing.T) {
	out := renderInstall(t, map[string]interface{}{
		"Component": "volume", "ComponentInstance": "volume0",
		"Binary": "weed-volume", "RustVolume": true, "Enterprise": true,
		"ReleaseOwner": "seaweedfs", "ReleaseRepo": "artifactory", "Version": "4.31",
	})
	if !strings.Contains(out, "weed-volume-enterprise_large_disk_${OS}_${SUFFIX}.tar.gz") {
		t.Errorf("enterprise rust path should download the weed-volume-enterprise_ asset")
	}
	if !strings.Contains(out, "seaweedfs/artifactory/releases") {
		t.Errorf("enterprise rust path should pull from the artifactory repo")
	}
	if strings.Contains(out, "weed-volume_large_disk_") {
		t.Errorf("enterprise rust path should not use the OSS asset name")
	}
}

func TestInstallScript_VersionedPath(t *testing.T) {
	out := renderInstall(t, map[string]interface{}{"Version": "4.31"})
	if !strings.Contains(out, `[ "$installedVersion" = "${SEAWEED_VERSION}" ]`) {
		t.Errorf("versioned path missing the version-compare skip check")
	}
	if strings.Contains(out, ".weed-dev-buildid") {
		t.Errorf("versioned path should not contain dev marker logic")
	}
}
