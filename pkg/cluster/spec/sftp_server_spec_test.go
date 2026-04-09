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
		"sshPrivateKey=/etc/seaweed/ssh_host_key\n" +
		"userStoreFile=/etc/seaweed/sftp.json\n" +
		"metricsPort=9876\n"
	if got := buf.String(); got != want {
		t.Fatalf("WriteToBuffer full options mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestSftpServerSpec_WriteToBuffer_ConfigExtraKeysSorted(t *testing.T) {
	s := &SftpServerSpec{
		Ip:    "10.0.0.5",
		Filer: "10.0.0.1:8888",
		Config: map[string]interface{}{
			"localSocket": "/tmp/sftp.sock",
			"cacheDir":    "/var/cache/sftp",
			"readOnly":    true,
		},
	}
	var buf bytes.Buffer
	s.WriteToBuffer(nil, &buf)

	want := "ip=10.0.0.5\n" +
		"filer=10.0.0.1:8888\n" +
		"cacheDir=/var/cache/sftp\n" +
		"localSocket=/tmp/sftp.sock\n" +
		"readOnly=true\n"
	if got := buf.String(); got != want {
		t.Fatalf("WriteToBuffer config mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestSftpServerSpec_WriteToBuffer_ConfigReservedKeysSkipped(t *testing.T) {
	s := &SftpServerSpec{
		Ip:          "10.0.0.5",
		Port:        2222,
		Filer:       "10.0.0.1:8888",
		HostKeyPath: "/etc/seaweed/ssh_host_key",
		AuthFile:    "/etc/seaweed/sftp.json",
		Config: map[string]interface{}{
			// All of these should be ignored since explicit fields own them.
			"ip":            "9.9.9.9",
			"port":          9999,
			"filer":         "evil:1",
			"sshPrivateKey": "/tmp/hijack_key",
			"userStoreFile": "/tmp/hijack_users",
			// Non-reserved extra passes through.
			"logLevel": "debug",
		},
	}
	var buf bytes.Buffer
	s.WriteToBuffer(nil, &buf)

	want := "ip=10.0.0.5\n" +
		"port=2222\n" +
		"filer=10.0.0.1:8888\n" +
		"sshPrivateKey=/etc/seaweed/ssh_host_key\n" +
		"userStoreFile=/etc/seaweed/sftp.json\n" +
		"logLevel=debug\n"
	if got := buf.String(); got != want {
		t.Fatalf("WriteToBuffer reserved-key guard mismatch:\n got: %q\nwant: %q", got, want)
	}
}
