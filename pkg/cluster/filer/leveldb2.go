package filer

// LevelDB2 is the default embedded filer store. It is used when no `type`
// key is specified in the filer configuration and requires no external
// services.
type LevelDB2 struct {
	// Enabled reports whether the leveldb2 backend should be turned on.
	// Defaults to true when constructed via FromConfig.
	Enabled bool `filer:"enabled"`
	// Dir is the on-disk directory used to store the leveldb2 database.
	Dir string `filer:"dir"`
}

const leveldb2Template = `[leveldb2]
enabled = {{.Enabled}}
dir = "{{.Dir}}"
`

func (l *LevelDB2) applyDefaults() {
	if l.Dir == "" {
		l.Dir = "/opt/seaweed/filerldb2"
	}
	// A freshly-constructed LevelDB2 is enabled by default. We only honor
	// a caller-supplied `enabled = false` via explicit configuration; the
	// zero value is promoted to true here.
	l.Enabled = true
}

// Name returns the canonical backend type name.
func (l *LevelDB2) Name() string { return "leveldb2" }

// Validate enforces that Dir is non-empty.
func (l *LevelDB2) Validate() error {
	if l.Dir == "" {
		return requiredErr("leveldb2", "dir")
	}
	return nil
}

// RenderTOML renders the leveldb2 section of filer.toml.
func (l *LevelDB2) RenderTOML() (string, error) {
	return render("leveldb2", leveldb2Template, l)
}
