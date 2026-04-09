// Package filer provides typed configuration backends for the SeaweedFS
// filer.toml file. Each supported storage backend implements the Backend
// interface and is responsible for validating its fields and rendering its
// corresponding section(s) of filer.toml.
package filer

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

// RenderOptions carries instance-scoped context used when materializing a
// filer.toml file. Backends that own host-local default paths (for example
// leveldb2's on-disk directory) derive those defaults from
// InstanceDataDir so that multiple filer instances on the same host get
// distinct directories.
type RenderOptions struct {
	// InstanceDataDir is the per-instance data directory for this filer,
	// for example "/opt/seaweed/filer0". It may be empty when rendering
	// outside of a deployment (for example in unit tests), in which case
	// backends fall back to their historical host-global defaults.
	InstanceDataDir string
}

// Backend represents a concrete filer storage backend that can render a
// filer.toml configuration file.
type Backend interface {
	// Name returns the canonical backend type name (for example "leveldb2",
	// "postgres", "mysql"). This is the value that appears under the
	// `type` key in the cluster YAML config.
	Name() string

	// Validate returns a non-nil error when the backend configuration is
	// missing required fields or contains invalid values.
	Validate() error

	// RenderTOML returns the textual filer.toml contents corresponding to
	// this backend. The output is deterministic and suitable for golden
	// file comparisons. opts supplies instance-scoped context such as the
	// per-instance data directory used to derive host-local path
	// defaults.
	RenderTOML(opts RenderOptions) (string, error)
}

// FromConfig constructs a Backend from the free-form configuration map
// stored in FilerServerSpec.Config. The map must contain a `type` key
// identifying the backend. If the map is nil or empty, the default
// LevelDB2 backend is returned.
func FromConfig(cfg map[string]interface{}) (Backend, error) {
	if len(cfg) == 0 {
		b := &LevelDB2{}
		b.applyDefaults()
		return b, nil
	}

	typeRaw, ok := cfg["type"]
	if !ok {
		b := &LevelDB2{}
		b.applyDefaults()
		if err := decodeInto(cfg, b); err != nil {
			return nil, err
		}
		if err := b.Validate(); err != nil {
			return nil, err
		}
		return b, nil
	}
	typeName, ok := typeRaw.(string)
	if !ok {
		return nil, fmt.Errorf("filer backend: 'type' must be a string, got %T", typeRaw)
	}

	var b Backend
	switch strings.ToLower(strings.TrimSpace(typeName)) {
	case "", "leveldb2":
		b = &LevelDB2{}
	case "postgres", "postgres2", "postgresql":
		b = &Postgres{}
	case "mysql", "mysql2":
		b = &MySQL{}
	case "redis2", "redis":
		b = &Redis2{}
	case "cassandra":
		b = &Cassandra{}
	case "tikv":
		b = &TiKV{}
	default:
		return nil, fmt.Errorf("filer backend: unknown type %q", typeName)
	}

	if d, ok := b.(interface{ applyDefaults() }); ok {
		d.applyDefaults()
	}
	if err := decodeInto(cfg, b); err != nil {
		return nil, err
	}
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return b, nil
}

// decodeInto copies values from the configuration map into the provided
// backend struct using a small case-insensitive reflect based mapping. We
// avoid pulling in mapstructure to keep the dependency graph minimal.
func decodeInto(cfg map[string]interface{}, into interface{}) error {
	// The set of supported field types is deliberately small.
	return reflectAssign(cfg, into)
}

// render executes a template against the given data and returns the
// resulting string, trimming trailing whitespace for stable output.
func render(name, tmpl string, data interface{}) (string, error) {
	t, err := template.New(name).Funcs(template.FuncMap{
		"tomlString": tomlString,
	}).Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("filer backend %s: parse template: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("filer backend %s: execute template: %w", name, err)
	}
	out := strings.TrimRight(buf.String(), " \t\n") + "\n"
	return out, nil
}

// tomlString renders s as a TOML basic string, including the surrounding
// double quotes. Backslash, double quote and control characters are
// escaped per the TOML 1.0 specification so that arbitrary user-supplied
// values (passwords, hostnames, paths) can safely be embedded into a
// quoted TOML value.
func tomlString(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

