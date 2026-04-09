package spec

import (
	"bytes"
	"testing"
)

func TestSftpServerSpec_WriteToBuffer_Defaults(t *testing.T) {
	s := &SftpServerSpec{
		Ip:    "10.0.0.5",
		Port:  2022,
		Filer: "10.0.0.1:8888",
	}
	var buf bytes.Buffer
	s.WriteToBuffer(nil, &buf)

	want := "ip=10.0.0.5\nfiler=10.0.0.1:8888\n"
	if got := buf.String(); got != want {
		t.Fatalf("WriteToBuffer default mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestSftpServerSpec_WriteToBuffer_AllOptions(t *testing.T) {
	s := &SftpServerSpec{
		Ip:          "10.0.0.5",
		IpBind:      "0.0.0.0",
		Port:        2222,
		Filer:       "10.0.0.1:8888",
		HostKeyPath: "/etc/seaweed/ssh_host_key",
		AuthFile:    "/etc/seaweed/sftp.json",
		MetricsPort: 9876,
	}
	var buf bytes.Buffer
	s.WriteToBuffer(nil, &buf)

	want := "ip=10.0.0.5\n" +
		"ip.bind=0.0.0.0\n" +
		"port=2222\n" +
		"filer=10.0.0.1:8888\n" +
		"sftp.host_key=/etc/seaweed/ssh_host_key\n" +
		"sftp.auth_file=/etc/seaweed/sftp.json\n" +
		"metricsPort=9876\n"
	if got := buf.String(); got != want {
		t.Fatalf("WriteToBuffer full options mismatch:\n got: %q\nwant: %q", got, want)
	}
}
