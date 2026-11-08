package spec

import (
	"bytes"
	"testing"
)

func TestVectorServerSpec_WriteToBuffer(t *testing.T) {
	v := &VectorServerSpec{Ip: "10.0.0.20", Masters: "10.0.0.1:9333,10.0.0.2:9333",
		Config: map[string]interface{}{"read-cache-mb": 1024, "listen": "0.0.0.0:1", "masters": "x"}}
	var buf bytes.Buffer
	v.WriteToBuffer(&buf)
	want := "listen=10.0.0.20:8090\nmasters=10.0.0.1:9333,10.0.0.2:9333\nread-cache-mb=1024\n"
	if got := buf.String(); got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	v = &VectorServerSpec{Ip: "fd00::20", Port: 9090, Masters: "m:9333"}
	if got, want := v.ListenAddress(), "[fd00::20]:9090"; got != want {
		t.Errorf("listen: got %q want %q", got, want)
	}
}
