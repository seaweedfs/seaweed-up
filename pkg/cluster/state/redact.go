package state

import (
	"strings"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"gopkg.in/yaml.v3"
)

// redactedSpec returns a deep copy of sp with every secret value cleared,
// so credentials are never written to the on-disk cluster state. The
// original sp is left untouched — callers keep using it (with real
// secrets) for the in-flight deploy; only the persisted copy is scrubbed.
//
// Redacted:
//   - global.bastion.password
//   - admin_servers[].admin_password
//   - any secret-looking key (password, secret, *_key, token, ...) inside
//     a component's free-form config / s3_config map, at any nesting depth
//     (filer DB credentials, the s3.json identities -> secretKey tree, ...)
//
// Consequence: name-based commands that re-render configs from saved state
// (e.g. `cluster upgrade <name>` without -f) will not have these secrets.
// Re-run such operations with -f <cluster.yaml> when they are needed.
func redactedSpec(sp *spec.Specification) (*spec.Specification, error) {
	// Deep copy via a YAML round-trip so redaction can't mutate the
	// caller's spec or alias its nested maps.
	b, err := yaml.Marshal(sp)
	if err != nil {
		return nil, err
	}
	clone := &spec.Specification{}
	if err := yaml.Unmarshal(b, clone); err != nil {
		return nil, err
	}

	if clone.GlobalOptions.Bastion != nil {
		clone.GlobalOptions.Bastion.Password = ""
	}
	for _, a := range clone.AdminServers {
		if a != nil {
			a.AdminPassword = ""
			redactMap(a.Config)
		}
	}
	for _, m := range clone.MasterServers {
		if m != nil {
			redactMap(m.Config)
		}
	}
	for _, v := range clone.VolumeServers {
		if v != nil {
			redactMap(v.Config)
		}
	}
	for _, f := range clone.FilerServers {
		if f != nil {
			redactMap(f.Config)
		}
	}
	for _, w := range clone.WorkerServers {
		if w != nil {
			redactMap(w.Config)
		}
	}
	for _, s := range clone.SftpServers {
		if s != nil {
			redactMap(s.Config)
		}
	}
	for _, s := range clone.S3Servers {
		if s != nil {
			redactMap(s.S3Config)
		}
	}

	return clone, nil
}

// redactMap blanks the value of any secret-looking key in m, recursing
// into nested maps and slices.
func redactMap(m map[string]interface{}) {
	for k, v := range m {
		if isSecretKey(k) {
			m[k] = ""
			continue
		}
		redactWalk(v)
	}
}

func redactWalk(v interface{}) {
	switch t := v.(type) {
	case map[string]interface{}:
		redactMap(t)
	case []interface{}:
		for _, e := range t {
			redactWalk(e)
		}
	}
}

// isSecretKey reports whether a config key name denotes a credential.
func isSecretKey(k string) bool {
	switch strings.ToLower(strings.TrimSpace(k)) {
	case "password", "passwd", "secret", "secret_key", "secretkey",
		"secretaccesskey", "access_key_secret", "token", "jwt_signing_key":
		return true
	}
	return false
}
