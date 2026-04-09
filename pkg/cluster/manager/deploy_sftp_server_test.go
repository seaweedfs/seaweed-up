package manager

import (
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestGenerateSSHHostKey(t *testing.T) {
	keyPEM, err := generateSSHHostKey()
	if err != nil {
		t.Fatalf("generateSSHHostKey returned error: %v", err)
	}
	if len(keyPEM) == 0 {
		t.Fatal("generateSSHHostKey returned empty bytes")
	}

	signer, err := ssh.ParsePrivateKey(keyPEM)
	if err != nil {
		t.Fatalf("ssh.ParsePrivateKey failed: %v", err)
	}
	if signer == nil {
		t.Fatal("ssh.ParsePrivateKey returned nil signer")
	}
	if signer.PublicKey() == nil {
		t.Fatal("signer has no public key")
	}
	if signer.PublicKey().Type() != ssh.KeyAlgoRSA {
		t.Fatalf("expected RSA key, got %s", signer.PublicKey().Type())
	}
}

func TestGenerateSSHHostKeyUnique(t *testing.T) {
	a, err := generateSSHHostKey()
	if err != nil {
		t.Fatalf("first generate: %v", err)
	}
	b, err := generateSSHHostKey()
	if err != nil {
		t.Fatalf("second generate: %v", err)
	}
	if string(a) == string(b) {
		t.Fatal("expected two calls to produce different keys")
	}
}
