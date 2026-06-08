package spec

import (
	"bytes"
	"strings"
	"testing"
)

func TestVolumeServerSpec_WriteToBuffer_DiskFlagPerEngine(t *testing.T) {
	mk := func(engine string) string {
		vs := &VolumeServerSpec{
			Ip: "10.0.0.1", Port: 8080, Engine: engine,
			Folders: []*FolderSpec{
				{Folder: "/data/d1", DiskType: "hdd"},
				{Folder: "/data/d2", DiskType: "hdd"},
			},
		}
		var buf bytes.Buffer
		vs.WriteToBuffer([]string{"10.0.0.1:9333"}, &buf)
		return buf.String()
	}
	// Go keeps the historical plural `disks`; rust must use the singular `disk`
	// (its clap rejects unknown flags).
	if got := mk(""); !strings.Contains(got, "disks=hdd,hdd\n") {
		t.Errorf("go engine should emit disks=, got: %q", got)
	}
	rust := mk("rust")
	if !strings.Contains(rust, "disk=hdd\n") || strings.Contains(rust, "disks=") {
		t.Errorf("rust engine should emit disk= (not disks=), got: %q", rust)
	}
}

func TestVolumeServerSpec_IsRust(t *testing.T) {
	cases := map[string]bool{"": false, "go": false, "weed": false, "rust": true, "weed-volume": true}
	for engine, want := range cases {
		if got := (&VolumeServerSpec{Engine: engine}).IsRust(); got != want {
			t.Errorf("IsRust(%q) = %v, want %v", engine, got, want)
		}
	}
}

func TestSpecification_Validate_VolumeEngine(t *testing.T) {
	base := func(engine string) *Specification {
		return &Specification{
			MasterServers: []*MasterServerSpec{{Ip: "10.0.0.1"}},
			VolumeServers: []*VolumeServerSpec{{Ip: "10.0.0.1", Engine: engine}},
		}
	}
	for _, ok := range []string{"", "go", "weed", "rust", "weed-volume"} {
		if err := base(ok).Validate(); err != nil {
			t.Errorf("engine %q should be valid, got %v", ok, err)
		}
	}
	if err := base("rusty").Validate(); err == nil {
		t.Error("engine \"rusty\" should be rejected")
	}

	// A null volume_servers list item must be rejected, not panic.
	nullEntry := &Specification{
		MasterServers: []*MasterServerSpec{{Ip: "10.0.0.1"}},
		VolumeServers: []*VolumeServerSpec{nil},
	}
	if err := nullEntry.Validate(); err == nil {
		t.Error("a null volume_servers entry should be rejected")
	}
}
