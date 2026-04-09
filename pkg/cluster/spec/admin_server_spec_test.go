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
	want := "ip=10.0.0.5\nmaster=10.0.0.1:9333,10.0.0.2:9333\n"
	if got := buf.String(); got != want {
		t.Fatalf("unexpected options output\n got: %q\nwant: %q", got, want)
	}
}

func TestAdminServerSpec_WriteToBuffer_CustomPortAndAuth(t *testing.T) {
	a := &AdminServerSpec{
		Ip:            "10.0.0.5",
		IpBind:        "0.0.0.0",
		Port:          24000,
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
		"master=10.0.0.1:9333\n" +
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

	want := "ip=10.0.0.5\nmaster=override1:9333,override2:9333\n"
	if got := buf.String(); got != want {
		t.Fatalf("unexpected options output\n got: %q\nwant: %q", got, want)
	}
}

func TestAdminServerSpec_WriteToBuffer_ConfigPassthrough(t *testing.T) {
	a := &AdminServerSpec{
		Ip:   "10.0.0.5",
		Port: 23646,
		Config: map[string]interface{}{
			"port.grpc": 33646,
			"urlPrefix": "/seaweedfs",
		},
	}
	masters := []string{"10.0.0.1:9333"}

	var buf bytes.Buffer
	a.WriteToBuffer(masters, &buf)

	// Config entries are emitted last in sorted key order.
	want := "ip=10.0.0.5\n" +
		"master=10.0.0.1:9333\n" +
		"port.grpc=33646\n" +
		"urlPrefix=/seaweedfs\n"
	if got := buf.String(); got != want {
		t.Fatalf("unexpected options output\n got: %q\nwant: %q", got, want)
	}
}
