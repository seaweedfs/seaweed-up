package manager

import (
	"strings"
	"testing"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

func TestValidateSftpFilerPrerequisite_NoSftp(t *testing.T) {
	s := &spec.Specification{}
	if err := validateSftpFilerPrerequisite(s); err != nil {
		t.Fatalf("expected no error for empty sftp, got %v", err)
	}
}

func TestValidateSftpFilerPrerequisite_FilerDefined(t *testing.T) {
	s := &spec.Specification{
		FilerServers: []*spec.FilerServerSpec{{Ip: "10.0.0.1", Port: 8888}},
		SftpServers:  []*spec.SftpServerSpec{{Ip: "10.0.0.5"}},
	}
	if err := validateSftpFilerPrerequisite(s); err != nil {
		t.Fatalf("expected no error when filer server exists, got %v", err)
	}
}

func TestValidateSftpFilerPrerequisite_ExplicitFiler(t *testing.T) {
	s := &spec.Specification{
		SftpServers: []*spec.SftpServerSpec{
			{Ip: "10.0.0.5", Filer: "external:8888"},
			{Ip: "10.0.0.6", Filer: "external:8888"},
		},
	}
	if err := validateSftpFilerPrerequisite(s); err != nil {
		t.Fatalf("expected no error when every sftp has explicit filer, got %v", err)
	}
}

func TestValidateSftpFilerPrerequisite_MissingFiler(t *testing.T) {
	s := &spec.Specification{
		SftpServers: []*spec.SftpServerSpec{
			{Ip: "10.0.0.5", Filer: "external:8888"},
			{Ip: "10.0.0.6"}, // missing
		},
	}
	err := validateSftpFilerPrerequisite(s)
	if err == nil {
		t.Fatalf("expected error when an sftp server lacks filer")
	}
	if !strings.Contains(err.Error(), "10.0.0.6") {
		t.Fatalf("error should mention offending host, got %v", err)
	}
}
