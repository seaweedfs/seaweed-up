package filer

// Postgres stores filer metadata in a PostgreSQL database using the
// postgres2 driver shipped with SeaweedFS.
type Postgres struct {
	Hostname string `filer:"hostname"`
	Port     int    `filer:"port"`
	Username string `filer:"username"`
	Password string `filer:"password"`
	Database string `filer:"database"`
	Schema   string `filer:"schema"`
	SslMode  string `filer:"sslmode"`
	// MaxOpenConns bounds the number of live connections. Optional.
	MaxOpenConns int `filer:"max_open_conns"`
	// MaxIdleConns bounds idle connections in the pool. Optional.
	MaxIdleConns int `filer:"max_idle_conns"`
	// ConnMaxLifetimeSeconds is an optional connection recycle period.
	ConnMaxLifetimeSeconds int `filer:"conn_max_lifetime_seconds"`
}

const postgresTemplate = `[postgres2]
enabled = true
createTable = """
  CREATE TABLE IF NOT EXISTS "%s" (
    dirhash   BIGINT,
    name      VARCHAR(65535),
    directory VARCHAR(65535),
    meta      bytea,
    PRIMARY KEY (dirhash, name)
  );
"""
hostname = {{tomlString .Hostname}}
port = {{.Port}}
username = {{tomlString .Username}}
password = {{tomlString .Password}}
database = {{tomlString .Database}}
schema = {{tomlString .Schema}}
sslmode = {{tomlString .SslMode}}
connection_max_idle = {{.MaxIdleConns}}
connection_max_open = {{.MaxOpenConns}}
connection_max_lifetime_seconds = {{.ConnMaxLifetimeSeconds}}
`

func (p *Postgres) applyDefaults() {
	if p.Port == 0 {
		p.Port = 5432
	}
	if p.SslMode == "" {
		p.SslMode = "disable"
	}
	if p.MaxIdleConns == 0 {
		p.MaxIdleConns = 2
	}
	if p.MaxOpenConns == 0 {
		p.MaxOpenConns = 100
	}
	if p.ConnMaxLifetimeSeconds == 0 {
		p.ConnMaxLifetimeSeconds = 0
	}
}

// Name returns the canonical backend type name.
func (p *Postgres) Name() string { return "postgres" }

// Validate enforces that host, user and database are set.
func (p *Postgres) Validate() error {
	if p.Hostname == "" {
		return requiredErr("postgres", "hostname")
	}
	if p.Username == "" {
		return requiredErr("postgres", "username")
	}
	if p.Database == "" {
		return requiredErr("postgres", "database")
	}
	return nil
}

// RenderTOML renders the postgres2 section of filer.toml.
func (p *Postgres) RenderTOML(_ RenderOptions) (string, error) {
	return render("postgres", postgresTemplate, p)
}
