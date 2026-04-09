package filer

// Redis2 stores filer metadata in a single Redis instance using the
// redis2 driver shipped with SeaweedFS.
type Redis2 struct {
	Address  string `filer:"address"`
	Password string `filer:"password"`
	Database int    `filer:"database"`
}

const redis2Template = `[redis2]
enabled = true
address = {{tomlString .Address}}
password = {{tomlString .Password}}
database = {{.Database}}
`

func (r *Redis2) applyDefaults() {
	if r.Address == "" {
		r.Address = "localhost:6379"
	}
}

// Name returns the canonical backend type name.
func (r *Redis2) Name() string { return "redis2" }

// Validate ensures a non-empty address.
func (r *Redis2) Validate() error {
	if r.Address == "" {
		return requiredErr("redis2", "address")
	}
	return nil
}

// RenderTOML renders the redis2 section of filer.toml.
func (r *Redis2) RenderTOML(_ RenderOptions) (string, error) {
	return render("redis2", redis2Template, r)
}
