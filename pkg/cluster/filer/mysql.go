package filer

// MySQL stores filer metadata in a MySQL compatible database using the
// mysql2 driver shipped with SeaweedFS.
type MySQL struct {
	Hostname               string `filer:"hostname"`
	Port                   int    `filer:"port"`
	Username               string `filer:"username"`
	Password               string `filer:"password"`
	Database               string `filer:"database"`
	MaxOpenConns           int    `filer:"max_open_conns"`
	MaxIdleConns           int    `filer:"max_idle_conns"`
	ConnMaxLifetimeSeconds int    `filer:"conn_max_lifetime_seconds"`
	InterpolateParams      bool   `filer:"interpolate_params"`
}

const mysqlTemplate = `[mysql2]
enabled = true
hostname = {{tomlString .Hostname}}
port = {{.Port}}
username = {{tomlString .Username}}
password = {{tomlString .Password}}
database = {{tomlString .Database}}
connection_max_idle = {{.MaxIdleConns}}
connection_max_open = {{.MaxOpenConns}}
connection_max_lifetime_seconds = {{.ConnMaxLifetimeSeconds}}
interpolateParams = {{.InterpolateParams}}
`

func (m *MySQL) applyDefaults() {
	if m.Port == 0 {
		m.Port = 3306
	}
	if m.MaxIdleConns == 0 {
		m.MaxIdleConns = 2
	}
	if m.MaxOpenConns == 0 {
		m.MaxOpenConns = 100
	}
}

// Name returns the canonical backend type name.
func (m *MySQL) Name() string { return "mysql" }

// Validate enforces required connection fields.
func (m *MySQL) Validate() error {
	if m.Hostname == "" {
		return requiredErr("mysql", "hostname")
	}
	if m.Username == "" {
		return requiredErr("mysql", "username")
	}
	if m.Database == "" {
		return requiredErr("mysql", "database")
	}
	return nil
}

// RenderTOML renders the mysql2 section of filer.toml.
func (m *MySQL) RenderTOML(_ RenderOptions) (string, error) {
	return render("mysql", mysqlTemplate, m)
}
