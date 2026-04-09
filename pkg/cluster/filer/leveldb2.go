package filer

import "path"

// LevelDB2 is the default embedded filer store. It is used when no `type`
// key is specified in the filer configuration and requires no external
// services.
type LevelDB2 struct {
	// Enabled reports whether the leveldb2 backend should be turned on.
	// Defaults to true when constructed via FromConfig.
	Enabled bool `filer:"enabled"`
	// Dir is the on-disk directory used to store the leveldb2 database.
	// When empty, the directory is derived at render time from the
	// per-instance data directory so that co-located filer instances do
	// not accidentally share a single metadata store.
	Dir string `filer:"dir"`
}

const leveldb2Template = `[leveldb2]
enabled = {{.Enabled}}
dir = {{tomlString .Dir}}
`

// leveldb2FallbackDir is the host-global default used only when no
// instance-scoped data directory is available (for example when
// rendering outside of a deploy flow).
const leveldb2FallbackDir = "/opt/seaweed/filerldb2"

func (l *LevelDB2) applyDefaults() {
	// A freshly-constructed LevelDB2 is enabled by default. We only honor
	// a caller-supplied `enabled = false` via explicit configuration; the
	// zero value is promoted to true here.
	l.Enabled = true
	// Dir is intentionally left as-is; the instance-scoped default is
	// resolved at render time so that it can depend on
	// RenderOptions.InstanceDataDir.
}

// resolveDir returns the effective on-disk directory for this leveldb2
// backend, deriving a per-instance default from opts when the caller did
// not provide an explicit Dir.
func (l *LevelDB2) resolveDir(opts RenderOptions) string {
	if l.Dir != "" {
		return l.Dir
	}
	if opts.InstanceDataDir != "" {
		return path.Join(opts.InstanceDataDir, "filerldb2")
	}
	return leveldb2FallbackDir
}

// Name returns the canonical backend type name.
func (l *LevelDB2) Name() string { return "leveldb2" }

// Validate accepts an empty Dir because the effective directory is
// resolved at render time from RenderOptions.InstanceDataDir.
func (l *LevelDB2) Validate() error {
	return nil
}

// RenderTOML renders the leveldb2 section of filer.toml.
func (l *LevelDB2) RenderTOML(opts RenderOptions) (string, error) {
	data := struct {
		Enabled bool
		Dir     string
	}{
		Enabled: l.Enabled,
		Dir:     l.resolveDir(opts),
	}
	return render("leveldb2", leveldb2Template, data)
}
