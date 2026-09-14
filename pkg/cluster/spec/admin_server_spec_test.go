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
	// how other specs emit options). dataDir defaults to "." so the admin's
	// maintenance scheduler can persist task state in its WorkingDirectory.
	want := "ip=10.0.0.5\nmaster=10.0.0.1:9333,10.0.0.2:9333\ndataDir=.\n"
	if got := buf.String(); got != want {
		t.Fatalf("unexpected options output\n got: %q\nwant: %q", got, want)
	}
}

// TestAdminServerSpec_WriteToBuffer_DataDirDefault is a regression guard: an
// admin deployed without an explicit dataDir must still get one ("."), or its
// maintenance scheduler can't persist task state and the EC balance/rebuild
// loop churns shards endlessly.
func TestAdminServerSpec_WriteToBuffer_DataDirDefault(t *testing.T) {
	a := &AdminServerSpec{Ip: "10.0.0.5", Port: 23646}
	var buf bytes.Buffer
	a.WriteToBuffer([]string{"10.0.0.1:9333"}, &buf)
	if !bytes.Contains(buf.Bytes(), []byte("dataDir=.\n")) {
		t.Fatalf("expected dataDir=. default, got: %q", buf.String())
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

// TestAdminServerSpec_WriteToBuffer_AllowInsecureBind guards the
// -allowInsecureBind pass-through: emitted when set, omitted when unset.
func TestAdminServerSpec_WriteToBuffer_AllowInsecureBind(t *testing.T) {
	a := &AdminServerSpec{Ip: "10.0.0.5", Port: 23646, AllowInsecureBind: true}
	var buf bytes.Buffer
	a.WriteToBuffer([]string{"10.0.0.1:9333"}, &buf)
	want := "ip=10.0.0.5\nmaster=10.0.0.1:9333\ndataDir=.\nallowInsecureBind=true\n"
	if got := buf.String(); got != want {
		t.Fatalf("enabled: unexpected options output\n got: %q\nwant: %q", got, want)
	}

	a = &AdminServerSpec{Ip: "10.0.0.5", Port: 23646}
	buf.Reset()
	a.WriteToBuffer([]string{"10.0.0.1:9333"}, &buf)
	want = "ip=10.0.0.5\nmaster=10.0.0.1:9333\ndataDir=.\n"
	if got := buf.String(); got != want {
		t.Fatalf("disabled: unexpected options output\n got: %q\nwant: %q", got, want)
	}
}

// TestAdminServerSpec_WriteToBuffer_AllowInsecureBindReserved ensures the
// first-class field supersedes a colliding Config entry.
func TestAdminServerSpec_WriteToBuffer_AllowInsecureBindReserved(t *testing.T) {
	a := &AdminServerSpec{
		Ip:                "10.0.0.5",
		Port:              23646,
		AllowInsecureBind: true,
		Config: map[string]interface{}{
			"allowInsecureBind": false,
			"port.grpc":         33646,
		},
	}
	var buf bytes.Buffer
	a.WriteToBuffer([]string{"10.0.0.1:9333"}, &buf)
	want := "ip=10.0.0.5\nmaster=10.0.0.1:9333\ndataDir=.\nallowInsecureBind=true\nport.grpc=33646\n"
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

	want := "ip=10.0.0.5\nmaster=override1:9333,override2:9333\ndataDir=.\n"
	if got := buf.String(); got != want {
		t.Fatalf("unexpected options output\n got: %q\nwant: %q", got, want)
	}
}

func TestAdminServerSpec_WriteToBuffer_ConfigReservedKeysSkipped(t *testing.T) {
	a := &AdminServerSpec{
		Ip:            "10.0.0.5",
		Port:          24000,
		DataDir:       "/var/lib/seaweed/admin",
		AdminUser:     "root",
		AdminPassword: "s3cret",
		Config: map[string]interface{}{
			// All of these collide with explicit fields and must be
			// dropped from the Config pass-through to avoid duplicate
			// flags in the generated options file.
			"ip":            "9.9.9.9",
			"ip.bind":       "0.0.0.0",
			"port":          9999,
			"master":        "bogus:9333",
			"dataDir":       "/tmp/bogus",
			"adminUser":     "other",
			"adminPassword": "other",
			// A non-reserved key should still flow through.
			"port.grpc": 33646,
		},
	}
	masters := []string{"10.0.0.1:9333"}

	var buf bytes.Buffer
	a.WriteToBuffer(masters, &buf)

	want := "ip=10.0.0.5\n" +
		"port=24000\n" +
		"master=10.0.0.1:9333\n" +
		"dataDir=/var/lib/seaweed/admin\n" +
		"adminUser=root\n" +
		"adminPassword=s3cret\n" +
		"port.grpc=33646\n"
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

	// Config entries are emitted last in sorted key order. dataDir defaults
	// to "." (after master, before the Config pass-through).
	want := "ip=10.0.0.5\n" +
		"master=10.0.0.1:9333\n" +
		"dataDir=.\n" +
		"port.grpc=33646\n" +
		"urlPrefix=/seaweedfs\n"
	if got := buf.String(); got != want {
		t.Fatalf("unexpected options output\n got: %q\nwant: %q", got, want)
	}
}
