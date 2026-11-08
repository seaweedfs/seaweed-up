package spec

import "testing"

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
	} {
		if got := tc.spec.RustAddress(); got != tc.want {
			t.Errorf("%+v: got %q want %q", tc.spec, got, tc.want)
		}
	}
}
