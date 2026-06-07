package state

import "testing"

func TestIsSecretKey(t *testing.T) {
	secret := []string{
		"password", "Password", "db_password", "passwd", "passphrase",
		"secret", "client_secret", "secretKey", "secret_key",
		"token", "access_token", "apikey", "api_key",
		"private_key", "signing_key", "encryption_key",
	}
	notSecret := []string{
		"type", "hostname", "port", "database", "username", "user",
		"accessKey", "access_key", "publicKey", "host_key_path",
		"key_prefix", "dir", "address",
	}
	for _, k := range secret {
		if !isSecretKey(k) {
			t.Errorf("isSecretKey(%q) = false, want true", k)
		}
	}
	for _, k := range notSecret {
		if isSecretKey(k) {
			t.Errorf("isSecretKey(%q) = true, want false", k)
		}
	}
}
