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

func TestInstallScript_VersionedPath(t *testing.T) {
	out := renderInstall(t, map[string]interface{}{"Version": "4.31"})
	if !strings.Contains(out, `[ "$installedVersion" = "${SEAWEED_VERSION}" ]`) {
		t.Errorf("versioned path missing the version-compare skip check")
	}
	if strings.Contains(out, ".weed-dev-buildid") {
		t.Errorf("versioned path should not contain dev marker logic")
	}
}
