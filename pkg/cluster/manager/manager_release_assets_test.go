package manager

import (
	"slices"
	"testing"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

func TestReleaseAssets(t *testing.T) {
	goVolumes := &spec.Specification{VolumeServers: []*spec.VolumeServerSpec{{}}}
	rustVolumes := &spec.Specification{VolumeServers: []*spec.VolumeServerSpec{{}, {Engine: "rust"}}}
	vectors := &spec.Specification{VolumeServers: []*spec.VolumeServerSpec{{}}, VectorServers: []*spec.VectorServerSpec{{}}}

	tests := []struct {
		name       string
		enterprise bool
		spec       *spec.Specification
		want       []string
	}{
		{name: "oss", spec: goVolumes, want: []string{
			"linux_amd64_full_large_disk.tar.gz", "linux_amd64_full_large_disk.tar.gz.md5",
			"linux_arm64_large_disk.tar.gz", "linux_arm64_large_disk.tar.gz.md5",
		}},
		{name: "enterprise", enterprise: true, spec: goVolumes, want: []string{
			"weed-enterprise-linux_amd64_large_disk.tar.gz", "weed-enterprise-linux_amd64_large_disk.tar.gz.md5",
			"weed-enterprise-linux_arm64_large_disk.tar.gz", "weed-enterprise-linux_arm64_large_disk.tar.gz.md5",
		}},
		{name: "oss rust volume", spec: rustVolumes, want: []string{
			"linux_amd64_full_large_disk.tar.gz", "linux_amd64_full_large_disk.tar.gz.md5",
			"weed-volume_large_disk_linux_amd64.tar.gz", "weed-volume_large_disk_linux_amd64.tar.gz.md5",
			"linux_arm64_large_disk.tar.gz", "linux_arm64_large_disk.tar.gz.md5",
			"weed-volume_large_disk_linux_arm64.tar.gz", "weed-volume_large_disk_linux_arm64.tar.gz.md5",
		}},
		{name: "enterprise rust volume", enterprise: true, spec: rustVolumes, want: []string{
			"weed-enterprise-linux_amd64_large_disk.tar.gz", "weed-enterprise-linux_amd64_large_disk.tar.gz.md5",
			"weed-volume-enterprise_large_disk_linux_amd64.tar.gz", "weed-volume-enterprise_large_disk_linux_amd64.tar.gz.md5",
			"weed-enterprise-linux_arm64_large_disk.tar.gz", "weed-enterprise-linux_arm64_large_disk.tar.gz.md5",
			"weed-volume-enterprise_large_disk_linux_arm64.tar.gz", "weed-volume-enterprise_large_disk_linux_arm64.tar.gz.md5",
		}},
		{name: "oss vector", spec: vectors, want: []string{
			"linux_amd64_full_large_disk.tar.gz", "linux_amd64_full_large_disk.tar.gz.md5",
			"weed-vector_linux_amd64.tar.gz", "weed-vector_linux_amd64.tar.gz.md5",
			"linux_arm64_large_disk.tar.gz", "linux_arm64_large_disk.tar.gz.md5",
			"weed-vector_linux_arm64.tar.gz", "weed-vector_linux_arm64.tar.gz.md5",
		}},
		{name: "enterprise vector", enterprise: true, spec: vectors, want: []string{
			"weed-enterprise-linux_amd64_large_disk.tar.gz", "weed-enterprise-linux_amd64_large_disk.tar.gz.md5",
			"weed-enterprise-linux_arm64_large_disk.tar.gz", "weed-enterprise-linux_arm64_large_disk.tar.gz.md5",
		}},
	}
	for _, tt := range tests {
		m := &Manager{Enterprise: tt.enterprise}
		if got := m.ReleaseAssets(tt.spec); !slices.Equal(got, tt.want) {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}
