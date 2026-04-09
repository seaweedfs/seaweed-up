package spec

import (
	"bytes"
	"testing"
)

func TestS3ServerSpec_WriteToBuffer_Defaults(t *testing.T) {
	s := &S3ServerSpec{
		Ip:    "10.0.0.1",
		Port:  8333,
		Filer: "10.0.0.2:8888",
	}
	var buf bytes.Buffer
	s.WriteToBuffer(&buf, "")

	got := buf.String()
	want := "ip=10.0.0.1\nfiler=10.0.0.2:8888\n"
	if got != want {
		t.Fatalf("unexpected options.\n got: %q\nwant: %q", got, want)
	}
}

func TestS3ServerSpec_WriteToBuffer_AllFields(t *testing.T) {
	s := &S3ServerSpec{
		Ip:          "10.0.0.1",
		IpBind:      "0.0.0.0",
		Port:        8443,
		PortGrpc:    18443,
		MetricsPort: 9091,
		Filer:       "10.0.0.2:8888",
	}
	var buf bytes.Buffer
	s.WriteToBuffer(&buf, "/etc/seaweed/s30.d/s3.json")

	got := buf.String()
	want := "ip=10.0.0.1\n" +
		"ip.bind=0.0.0.0\n" +
		"port=8443\n" +
		"port.grpc=18443\n" +
		"metricsPort=9091\n" +
		"filer=10.0.0.2:8888\n" +
		"config=/etc/seaweed/s30.d/s3.json\n"
	if got != want {
		t.Fatalf("unexpected options.\n got: %q\nwant: %q", got, want)
	}
}
