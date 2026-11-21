package spec

import (
	"bytes"
	"strings"
	"testing"
)

func TestMasterServerSpec_RustAddress(t *testing.T) {
	for _, tc := range []struct {
		spec MasterServerSpec
		want string
	}{
		{MasterServerSpec{Ip: "10.0.0.1"}, "10.0.0.1:9333"},
		{MasterServerSpec{Ip: "10.0.0.1", Port: 9333, PortGrpc: 19333}, "10.0.0.1:9333"},
		{MasterServerSpec{Ip: "10.0.0.1", Port: 9400}, "10.0.0.1:9400"},
		// A gRPC port off the +10000 convention travels as host:http.grpc.
		{MasterServerSpec{Ip: "10.0.0.1", Port: 9333, PortGrpc: 29333}, "10.0.0.1:9333.29333"},
		// The Rust clients split host from port at the last colon.
		{MasterServerSpec{Ip: "fd00::1", Port: 9333}, "[fd00::1]:9333"},
		{MasterServerSpec{Ip: "fd00::1", Port: 9333, PortGrpc: 29333}, "[fd00::1]:9333.29333"},
	} {
		if got := tc.spec.RustAddress(); got != tc.want {
			t.Errorf("%+v: got %q want %q", tc.spec, got, tc.want)
		}
	}
}

// A master's config entries reach its options file; ones that would repeat
// an option written from a field are left out.
func TestMasterServerSpec_WriteToBufferConfig(t *testing.T) {
	m := &MasterServerSpec{Ip: "10.0.0.1", Port: 9333, Config: map[string]interface{}{
		"deletionRetention": "-1s",
		"garbageThreshold":  0.4,
		"port":              9999,
	}}
	var buf bytes.Buffer
	m.WriteToBuffer([]string{"10.0.0.1:9333"}, &buf)
	got := buf.String()
	for _, want := range []string{"\ndeletionRetention=-1s\n", "\ngarbageThreshold=0.4\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("options missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "port=9999") {
		t.Errorf("a config entry repeated the port option:\n%s", got)
	}
	if strings.Index(got, "deletionRetention") > strings.Index(got, "garbageThreshold") {
		t.Errorf("config entries are not sorted:\n%s", got)
	}
}
