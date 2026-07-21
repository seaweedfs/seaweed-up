package config

import "testing"

func TestPickRelease(t *testing.T) {
	// Mirrors the enterprise release repo, which interleaves seaweed-vfs
	// client releases with weed server releases, newest first.
	releases := []Release{
		{TagName: "vfs-0.1.6"},
		{TagName: "4.40"},
		{TagName: "vfs-0.1.5"},
		{TagName: "4.39"},
	}

	tests := []struct {
		name      string
		ver       string
		tagFilter func(string) bool
		want      string
	}{
		{name: "latest skips non-weed tags", ver: "0", tagFilter: IsWeedReleaseTag, want: "4.40"},
		{name: "latest without filter", ver: "0", want: "vfs-0.1.6"},
		{name: "exact version", ver: "4.39", tagFilter: IsWeedReleaseTag, want: "4.39"},
		{name: "unknown version", ver: "9.99", want: ""},
	}
	for _, tt := range tests {
		if got := pickRelease(releases, tt.ver, tt.tagFilter).TagName; got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}

	if got := pickRelease(nil, "0", nil).TagName; got != "" {
		t.Errorf("empty release list: got %q, want empty", got)
	}
}

func TestIsWeedReleaseTag(t *testing.T) {
	for tag, want := range map[string]bool{
		"4.40":      true,
		"3.59":      true,
		"vfs-0.1.6": false,
		"dev":       false,
	} {
		if got := IsWeedReleaseTag(tag); got != want {
			t.Errorf("IsWeedReleaseTag(%q) = %v, want %v", tag, got, want)
		}
	}
}
