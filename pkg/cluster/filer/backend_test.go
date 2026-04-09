package filer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goldenCase describes a single backend configuration whose rendered
// TOML output is compared against a golden file on disk.
type goldenCase struct {
	name   string
	golden string
	cfg    map[string]interface{}
}

func TestBackendFromConfigGolden(t *testing.T) {
	cases := []goldenCase{
		{
			name:   "leveldb2_default",
			golden: "leveldb2.toml.golden",
			cfg:    nil,
		},
		{
			name:   "leveldb2_custom_dir",
			golden: "leveldb2_custom.toml.golden",
			cfg: map[string]interface{}{
				"type": "leveldb2",
				"dir":  "/var/lib/seaweedfs/ldb2",
			},
		},
		{
			name:   "postgres",
			golden: "postgres.toml.golden",
			cfg: map[string]interface{}{
				"type":     "postgres",
				"hostname": "pg.internal",
				"port":     5432,
				"username": "seaweed",
				"password": "s3cret",
				"database": "seaweedfs",
				"schema":   "public",
			},
		},
		{
			name:   "mysql",
			golden: "mysql.toml.golden",
			cfg: map[string]interface{}{
				"type":     "mysql",
				"hostname": "mysql.internal",
				"username": "root",
				"password": "pw",
				"database": "seaweedfs",
			},
		},
		{
			name:   "redis2",
			golden: "redis2.toml.golden",
			cfg: map[string]interface{}{
				"type":     "redis2",
				"address":  "redis.internal:6379",
				"password": "pw",
				"database": 1,
			},
		},
		{
			name:   "cassandra",
			golden: "cassandra.toml.golden",
			cfg: map[string]interface{}{
				"type":     "cassandra",
				"keyspace": "seaweed",
				"hosts":    []interface{}{"c1", "c2", "c3"},
				"username": "cass",
				"password": "pw",
			},
		},
		{
			name:   "tikv",
			golden: "tikv.toml.golden",
			cfg: map[string]interface{}{
				"type":                    "tikv",
				"pdaddrs":                 []interface{}{"pd1:2379", "pd2:2379"},
				"deleterange_concurrency": 4,
			},
		},
	}

	update := os.Getenv("UPDATE_GOLDEN") == "1"

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			b, err := FromConfig(tc.cfg)
			if err != nil {
				t.Fatalf("FromConfig: %v", err)
			}
			got, err := b.RenderTOML()
			if err != nil {
				t.Fatalf("RenderTOML: %v", err)
			}
			path := filepath.Join("testdata", tc.golden)
			if update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}
			wantBytes, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %s: %v", path, err)
			}
			if got != string(wantBytes) {
				t.Errorf("golden mismatch for %s\n--- want ---\n%s\n--- got ---\n%s", tc.golden, string(wantBytes), got)
			}
		})
	}
}

func TestFromConfigDefaultsLevelDB2(t *testing.T) {
	b, err := FromConfig(nil)
	if err != nil {
		t.Fatalf("FromConfig(nil): %v", err)
	}
	if b.Name() != "leveldb2" {
		t.Fatalf("expected leveldb2, got %s", b.Name())
	}
	if err := b.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestFromConfigUnknownType(t *testing.T) {
	_, err := FromConfig(map[string]interface{}{"type": "nosuch"})
	if err == nil || !strings.Contains(err.Error(), "unknown type") {
		t.Fatalf("expected unknown type error, got %v", err)
	}
}

func TestFromConfigUnknownKey(t *testing.T) {
	_, err := FromConfig(map[string]interface{}{
		"type":     "postgres",
		"hostname": "h",
		"username": "u",
		"database": "d",
		"bogus":    "x",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown config key") {
		t.Fatalf("expected unknown config key error, got %v", err)
	}
}

func TestValidateRequiredFields(t *testing.T) {
	cases := []struct {
		name string
		cfg  map[string]interface{}
		msg  string
	}{
		{
			name: "postgres_missing_hostname",
			cfg:  map[string]interface{}{"type": "postgres", "username": "u", "database": "d"},
			msg:  "hostname",
		},
		{
			name: "mysql_missing_database",
			cfg:  map[string]interface{}{"type": "mysql", "hostname": "h", "username": "u"},
			msg:  "database",
		},
		{
			name: "tikv_missing_pdaddrs",
			cfg:  map[string]interface{}{"type": "tikv"},
			msg:  "pdaddrs",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := FromConfig(tc.cfg)
			if err == nil || !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("expected error containing %q, got %v", tc.msg, err)
			}
		})
	}
}
