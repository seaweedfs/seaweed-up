package spec

import (
	"bytes"
	"strings"
	"testing"
)

// Both Go `weed volume` and Rust `weed-volume` take the disk-type flag as
// -disk (singular). The options key must therefore be "disk", not "disks":
// Go's lenient -options reader ignored the plural key, but the Rust binary's
// strict parser rejects it ("unexpected argument '--disks'") and crash-loops.
func TestVolumeServerSpec_WriteToBuffer_DiskKeyIsSingular(t *testing.T) {
	vs := &VolumeServerSpec{
		Ip:   "10.0.0.1",
		Port: 8080,
		Folders: []*FolderSpec{
			{Folder: "/data/v1/disk1", DiskType: "hdd", Max: 100},
			{Folder: "/data/v1/disk2", DiskType: "ssd", Max: 50},
		},
	}
	var buf bytes.Buffer
	vs.WriteToBuffer([]string{"10.0.0.1:9333"}, &buf)
	got := buf.String()

	if strings.Contains(got, "disks=") {
		t.Errorf("options must not use the plural 'disks=' key; Rust weed-volume rejects it:\n%s", got)
	}
	if !strings.Contains(got, "disk=hdd,ssd\n") {
		t.Errorf("expected singular 'disk=hdd,ssd', got:\n%s", got)
	}
}
