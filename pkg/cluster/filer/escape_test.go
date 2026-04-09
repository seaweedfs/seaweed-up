package filer

import (
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

// TestTomlStringEscapes verifies that tomlString produces valid TOML
// basic strings for payloads that contain characters with TOML-level
// meaning (backslash, quote) as well as control characters.
func TestTomlStringEscapes(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{``, `""`},
		{`plain`, `"plain"`},
		{`a"b`, `"a\"b"`},
		{`a\b`, `"a\\b"`},
		{"a\nb", `"a\nb"`},
		{"a\tb", `"a\tb"`},
		{"a\rb", `"a\rb"`},
		{"a\x00b", `"a\u0000b"`},
		{"a\x7fb", `"a\u007Fb"`},
	}
	for _, tc := range cases {
		if got := tomlString(tc.in); got != tc.want {
			t.Errorf("tomlString(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

// TestBackendTomlEscapesParse renders every backend with a payload
// containing TOML metacharacters and verifies the resulting filer.toml
// round-trips through a real TOML parser.
func TestBackendTomlEscapesParse(t *testing.T) {
	nasty := "pa\"ss\\word\nline"

	cases := []struct {
		name    string
		cfg     map[string]interface{}
		section string
		key     string
	}{
		{
			name: "postgres",
			cfg: map[string]interface{}{
				"type":     "postgres",
				"hostname": "pg.internal",
				"username": "seaweed",
				"password": nasty,
				"database": "seaweedfs",
			},
			section: "postgres2",
			key:     "password",
		},
		{
			name: "mysql",
			cfg: map[string]interface{}{
				"type":     "mysql",
				"hostname": "mysql.internal",
				"username": "root",
				"password": nasty,
				"database": "seaweedfs",
			},
			section: "mysql2",
			key:     "password",
		},
		{
			name: "redis2",
			cfg: map[string]interface{}{
				"type":     "redis2",
				"address":  "redis.internal:6379",
				"password": nasty,
			},
			section: "redis2",
			key:     "password",
		},
		{
			name: "cassandra",
			cfg: map[string]interface{}{
				"type":     "cassandra",
				"keyspace": "seaweed",
				"hosts":    []interface{}{"c1", "c2"},
				"username": "cass",
				"password": nasty,
			},
			section: "cassandra",
			key:     "password",
		},
		{
			name: "leveldb2",
			cfg: map[string]interface{}{
				"type": "leveldb2",
				"dir":  "/var/weird\"path\\x",
			},
			section: "leveldb2",
			key:     "dir",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			b, err := FromConfig(tc.cfg)
			if err != nil {
				t.Fatalf("FromConfig: %v", err)
			}
			out, err := b.RenderTOML(RenderOptions{})
			if err != nil {
				t.Fatalf("RenderTOML: %v", err)
			}
			var decoded map[string]map[string]interface{}
			if err := toml.Unmarshal([]byte(out), &decoded); err != nil {
				t.Fatalf("parse rendered TOML: %v\n---\n%s", err, out)
			}
			section, ok := decoded[tc.section]
			if !ok {
				t.Fatalf("section %q missing in:\n%s", tc.section, out)
			}
			got, ok := section[tc.key].(string)
			if !ok {
				t.Fatalf("key %q not a string in section %q: %v", tc.key, tc.section, section[tc.key])
			}
			want := tc.cfg[tc.key].(string)
			if got != want {
				t.Errorf("round trip mismatch for %s/%s\nwant: %q\n got: %q", tc.section, tc.key, want, got)
			}
		})
	}
}

// TestTiKVTomlEscapesParse verifies that PD addresses with quotes and
// backslashes survive template rendering through a real TOML parser.
func TestTiKVTomlEscapesParse(t *testing.T) {
	b, err := FromConfig(map[string]interface{}{
		"type":    "tikv",
		"pdaddrs": []interface{}{"pd\"1:2379", "pd\\2:2379"},
	})
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}
	out, err := b.RenderTOML(RenderOptions{})
	if err != nil {
		t.Fatalf("RenderTOML: %v", err)
	}
	var decoded struct {
		TiKV struct {
			PdAddrs []string `toml:"pdaddrs"`
		} `toml:"tikv"`
	}
	if err := toml.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("parse rendered TOML: %v\n---\n%s", err, out)
	}
	if len(decoded.TiKV.PdAddrs) != 2 || decoded.TiKV.PdAddrs[0] != "pd\"1:2379" || decoded.TiKV.PdAddrs[1] != "pd\\2:2379" {
		t.Errorf("round trip mismatch: %#v\n---\n%s", decoded.TiKV.PdAddrs, out)
	}
}

// TestLevelDB2PerInstanceDir verifies that the leveldb2 default
// directory is derived from RenderOptions.InstanceDataDir so that
// co-located filers get distinct metadata stores.
func TestLevelDB2PerInstanceDir(t *testing.T) {
	b, err := FromConfig(nil)
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}
	out, err := b.RenderTOML(RenderOptions{InstanceDataDir: "/opt/seaweed/filer0"})
	if err != nil {
		t.Fatalf("RenderTOML: %v", err)
	}
	if !strings.Contains(out, `dir = "/opt/seaweed/filer0/filerldb2"`) {
		t.Errorf("expected per-instance dir in output:\n%s", out)
	}

	out2, err := b.RenderTOML(RenderOptions{InstanceDataDir: "/opt/seaweed/filer1"})
	if err != nil {
		t.Fatalf("RenderTOML: %v", err)
	}
	if !strings.Contains(out2, `dir = "/opt/seaweed/filer1/filerldb2"`) {
		t.Errorf("expected distinct per-instance dir in output:\n%s", out2)
	}

	// Explicit Dir overrides the instance-derived default.
	b2, err := FromConfig(map[string]interface{}{"type": "leveldb2", "dir": "/custom"})
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}
	out3, err := b2.RenderTOML(RenderOptions{InstanceDataDir: "/opt/seaweed/filer0"})
	if err != nil {
		t.Fatalf("RenderTOML: %v", err)
	}
	if !strings.Contains(out3, `dir = "/custom"`) {
		t.Errorf("expected explicit dir override:\n%s", out3)
	}

	// Empty InstanceDataDir falls back to the host-global default.
	out4, err := b.RenderTOML(RenderOptions{})
	if err != nil {
		t.Fatalf("RenderTOML: %v", err)
	}
	if !strings.Contains(out4, `dir = "/opt/seaweed/filerldb2"`) {
		t.Errorf("expected fallback dir in output:\n%s", out4)
	}
}
