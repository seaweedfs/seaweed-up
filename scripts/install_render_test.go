package scripts

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
		"RustDevAssetURL": "", "RustDevMd5URL": "", "RustDevBuildID": "",
		"Binary": "weed", "RustVolume": false, "Enterprise": false,
		"RustWorker": false, "RustWorkerArgs": "",
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
		"weed-volume_large_disk_${OS}_${SUFFIX}.tar.gz",      // versioned per-arch release asset
		".weed-volume-version",                               // version marker
		`RUST_VERSION_ID="${SEAWEED_VERSION}:${RUST_ASSET}"`, // composite key: version + flavor
		"ExecStart=${BIN_DIR}/${BINARY} --options=",          // double-dash --options (Rust binary); no `weed volume` subcommand / go globals
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
	// Stable releases ship a .md5, so checksum verification is mandatory:
	// the best-effort skip notice must not appear on the stable path.
	if strings.Contains(out, "skipping checksum verification") {
		t.Errorf("stable rust path must verify md5, not skip it")
	}
}

func TestInstallScript_RustVolumeDevPath(t *testing.T) {
	out := renderInstall(t, map[string]interface{}{
		"Component": "volume", "ComponentInstance": "volume0",
		"Binary": "weed-volume", "RustVolume": true, "Version": "dev",
		"RustDevAssetURL": "https://github.com/seaweedfs/seaweedfs/releases/download/dev/weed-volume-large-disk-20260613-0656-linux-amd64.tar.gz",
		"RustDevMd5URL":   "https://github.com/seaweedfs/seaweedfs/releases/download/dev/weed-volume-large-disk-20260613-0656-linux-amd64.tar.gz.md5",
		"RustDevBuildID":  "20260613-0656",
	})
	for _, want := range []string{
		"BINARY=weed-volume",
		"weed-volume-large-disk-20260613-0656-linux-amd64.tar.gz", // resolved dev asset URL
		`RUST_VERSION_ID="20260613-0656"`,                         // keyed on dev build id, not "dev"
		".weed-volume-version",
		"skipping checksum verification", // best-effort md5 (dev lacks .md5)
		`[ "$md5Code" = "404" ]`,         // only a genuine 404 is a soft skip
		"failed to fetch .md5",           // other md5 errors are fatal, not silent
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rust dev install script missing %q", want)
		}
	}
	// must NOT construct the stable per-arch asset name with version=dev
	if strings.Contains(out, "weed-volume_large_disk_${OS}_${SUFFIX}.tar.gz") {
		t.Errorf("rust dev path should use the resolved dev asset, not the stable asset name")
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

func TestInstallScript_RustWorkerPath(t *testing.T) {
	out := renderInstall(t, map[string]interface{}{
		"Component": "worker-lance", "ComponentInstance": "worker-lance0",
		"Binary": "weed-worker", "RustWorker": true, "Version": "4.44",
		"RustWorkerArgs": "--admin 10.0.0.61:23646 --namespace http://10.0.0.51:9101",
	})
	for _, want := range []string{
		"BINARY=weed-worker",
		"weed-worker_${OS}_${SUFFIX}.tar.gz", // versioned per-arch release asset
		".weed-worker-version",               // version marker
		`WORKER_VERSION_ID="${SEAWEED_VERSION}:${WORKER_ASSET}"`,
		"weed-worker is published for linux amd64/arm64 only", // arch guard
		"md5sum -c",
		`"$TMP_DIR/weed-worker" --help`, // glibc probe before install
		"skipping the Lance maintenance worker",
		// flags on the ExecStart line: weed-worker reads no options file
		"ExecStart=${BIN_DIR}/${BINARY} --admin 10.0.0.61:23646 --namespace http://10.0.0.51:9101",
		"WorkingDirectory=${SEAWEED_COMPONENT_INSTANCE_DATA_DIR}\n",
		// the admin signing key, kept off the command line
		"EnvironmentFile=-${SEAWEED_COMPONENT_INSTANCE_CONFIG_DIR}/security.env\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rust worker install script missing %q", want)
		}
	}
	// must not take the Go weed download path or the go-style ExecStart
	if strings.Contains(out, "_full_large_disk.tar.gz") {
		t.Errorf("rust worker path should not download the Go weed tarball")
	}
	if strings.Contains(out, "-logdir=") || strings.Contains(out, "${COMPONENT} -options=") {
		t.Errorf("rust worker ExecStart should not use go-style flags / subcommand")
	}
	if strings.Contains(out, "weed-volume") {
		t.Errorf("rust worker path should not reference weed-volume")
	}
	if goUnit := renderInstall(t, map[string]interface{}{"Component": "worker"}); strings.Contains(goUnit, "EnvironmentFile") {
		t.Errorf("go units read security.toml from -config_dir and need no environment file")
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

func TestInstallScript_RecreatesKeyFiles(t *testing.T) {
	out := renderInstall(t, map[string]interface{}{"Component": "worker"})
	_, body, ok := strings.Cut(out, "create_user_and_config() {")
	if !ok {
		t.Fatal("create_user_and_config not found")
	}
	body, _, _ = strings.Cut(body, "\n}\n")

	dir := t.TempDir()
	shared := filepath.Join(dir, "security.toml")
	configDir := filepath.Join(dir, "worker0.d")
	tmpConfig := filepath.Join(dir, "tmp", "config")
	for _, d := range []string{configDir, tmpConfig} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(shared, []byte("shared"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpConfig, "security.toml"), []byte("instance"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(configDir, "security.toml")
	if err := os.Symlink(shared, target); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpConfig, "security.env"), []byte("KEY=new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(configDir, "security.env")
	if err := os.WriteFile(envFile, []byte("KEY=old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	script := "info() { :; }\nSUDO=\nTMP_DIR=" + filepath.Join(dir, "tmp") +
		"\nSEAWEED_COMPONENT_INSTANCE_CONFIG_DIR=" + configDir +
		"\nSEAWEED_COMPONENT_INSTANCE_DATA_DIR=" + filepath.Join(dir, "data") +
		"\ncreate_user_and_config() {" + body + "\n}\ncreate_user_and_config\n"
	if b, err := exec.Command("sh", "-ec", script).CombinedOutput(); err != nil {
		t.Fatalf("create_user_and_config: %v\n%s", err, b)
	}
	if got, _ := os.ReadFile(shared); string(got) != "shared" {
		t.Errorf("shared security.toml was overwritten: %q", got)
	}
	if fi, err := os.Lstat(target); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("instance security.toml should be a regular file: %v %v", fi, err)
	}
	if got, _ := os.ReadFile(target); string(got) != "instance" {
		t.Errorf("instance security.toml = %q, want the rendered file", got)
	}
	for _, f := range []string{target, envFile} {
		if fi, err := os.Stat(f); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("%s must be owner-only after install: %v %v", f, fi.Mode(), err)
		}
	}
	if got, _ := os.ReadFile(envFile); string(got) != "KEY=new\n" {
		t.Errorf("security.env = %q, want the rendered file", got)
	}
}
