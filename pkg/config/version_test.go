package config

import "testing"

func TestPickRelease(t *testing.T) {
	// Mirrors the enterprise release repo, which interleaves seaweed-vfs
	// client releases with weed server releases, newest first. 4.42 is
	// published with no binaries yet, 4.41 is still uploading them.
	const amd64, arm64 = "weed-enterprise-linux_amd64_large_disk.tar.gz", "weed-enterprise-linux_arm64_large_disk.tar.gz"
	both := []Asset{{Name: amd64}, {Name: arm64}}
	releases := []Release{
		{TagName: "4.42"},
		{TagName: "4.41", Assets: []Asset{{Name: arm64}}},
		{TagName: "vfs-0.1.6", Assets: []Asset{{Name: "sw-vfs.tar.gz"}}},
		{TagName: "4.40", Assets: both},
		{TagName: "vfs-0.1.5", Assets: []Asset{{Name: "sw-vfs.tar.gz"}}},
		{TagName: "4.39", Assets: both},
	}

	tests := []struct {
		name      string
		ver       string
		tagFilter func(string) bool
		required  []string
		want      string
	}{
		{name: "latest skips releases without assets", ver: "0", tagFilter: IsWeedReleaseTag, want: "4.41"},
		{name: "latest skips releases missing a required asset", ver: "0", tagFilter: IsWeedReleaseTag, required: []string{amd64, arm64}, want: "4.40"},
		{name: "latest without filter", ver: "0", want: "4.41"},
		{name: "exact version", ver: "4.39", tagFilter: IsWeedReleaseTag, want: "4.39"},
		{name: "exact version still uploading", ver: "4.42", tagFilter: IsWeedReleaseTag, required: []string{amd64}, want: "4.42"},
		{name: "unknown version", ver: "9.99", want: ""},
	}
	for _, tt := range tests {
		if got := pickRelease(releases, tt.ver, tt.tagFilter, tt.required).TagName; got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}

	if got := pickRelease(nil, "0", nil, nil).TagName; got != "" {
		t.Errorf("empty release list: got %q, want empty", got)
	}
}

func TestIsWeedReleaseTag(t *testing.T) {
	for tag, want := range map[string]bool{
		"4.40":      true,
		"v4.40":     true,
		"3.59":      true,
		"vfs-0.1.6": false,
		"dev":       false,
	} {
		if got := IsWeedReleaseTag(tag); got != want {
			t.Errorf("IsWeedReleaseTag(%q) = %v, want %v", tag, got, want)
		}
	}
}
