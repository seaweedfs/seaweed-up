package spec

import (
	"bytes"
	"testing"
)

func TestAdminServerSpec_WriteToBuffer_Defaults(t *testing.T) {
	a := &AdminServerSpec{
		Ip:   "10.0.0.5",
		Port: 23646,
	}
	masters := []string{"10.0.0.1:9333", "10.0.0.2:9333"}

	var buf bytes.Buffer
	a.WriteToBuffer(masters, &buf)

	// At the default port the `port=` line should be suppressed (matches
	// how other specs emit options).
	want := "ip=10.0.0.5\nmasters=10.0.0.1:9333,10.0.0.2:9333\n"
	if got := buf.String(); got != want {
		t.Fatalf("unexpected options output\n got: %q\nwant: %q", got, want)
	}
}

func TestAdminServerSpec_WriteToBuffer_CustomPortAndFiler(t *testing.T) {
	a := &AdminServerSpec{
		Ip:            "10.0.0.5",
		IpBind:        "0.0.0.0",
		Port:          24000,
		Filer:         "10.0.0.4:8888",
		DataDir:       "/var/lib/seaweed/admin",
		AdminUser:     "root",
		AdminPassword: "s3cret",
	}
	masters := []string{"10.0.0.1:9333"}

	var buf bytes.Buffer
	a.WriteToBuffer(masters, &buf)

	want := "ip=10.0.0.5\n" +
		"ip.bind=0.0.0.0\n" +
		"port=24000\n" +
		"masters=10.0.0.1:9333\n" +
		"filer=10.0.0.4:8888\n" +
		"dataDir=/var/lib/seaweed/admin\n" +
		"adminUser=root\n" +
		"adminPassword=s3cret\n"
	if got := buf.String(); got != want {
		t.Fatalf("unexpected options output\n got: %q\nwant: %q", got, want)
	}
}

func TestAdminServerSpec_WriteToBuffer_MastersOverride(t *testing.T) {
	a := &AdminServerSpec{
		Ip:      "10.0.0.5",
		Port:    23646,
		Masters: []string{"override1:9333", "override2:9333"},
	}
	masters := []string{"ignored:9333"}

	var buf bytes.Buffer
	a.WriteToBuffer(masters, &buf)

	want := "ip=10.0.0.5\nmasters=override1:9333,override2:9333\n"
	if got := buf.String(); got != want {
		t.Fatalf("unexpected options output\n got: %q\nwant: %q", got, want)
	}
}
