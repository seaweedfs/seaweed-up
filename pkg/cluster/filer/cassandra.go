package filer

import "strings"

// Cassandra stores filer metadata in an Apache Cassandra cluster.
type Cassandra struct {
	// Keyspace is the Cassandra keyspace holding the filer tables.
	Keyspace string `filer:"keyspace"`
	// Hosts is the list of Cassandra contact points.
	Hosts []string `filer:"hosts"`
	// Username is used for authenticated connections. Optional.
	Username string `filer:"username"`
	// Password is used for authenticated connections. Optional.
	Password string `filer:"password"`
}

const cassandraTemplate = `[cassandra]
enabled = true
keyspace = {{tomlString .Keyspace}}
hosts = [{{.HostsLiteral}}]
username = {{tomlString .Username}}
password = {{tomlString .Password}}
`

type cassandraTmplData struct {
	*Cassandra
	HostsLiteral string
}

func (c *Cassandra) applyDefaults() {
	if c.Keyspace == "" {
		c.Keyspace = "seaweedfs"
	}
	if len(c.Hosts) == 0 {
		c.Hosts = []string{"localhost"}
	}
}

// Name returns the canonical backend type name.
func (c *Cassandra) Name() string { return "cassandra" }

// Validate ensures keyspace and at least one host are set.
func (c *Cassandra) Validate() error {
	if c.Keyspace == "" {
		return requiredErr("cassandra", "keyspace")
	}
	if len(c.Hosts) == 0 {
		return requiredErr("cassandra", "hosts")
	}
	return nil
}

// RenderTOML renders the cassandra section of filer.toml.
func (c *Cassandra) RenderTOML(_ RenderOptions) (string, error) {
	quoted := make([]string, 0, len(c.Hosts))
	for _, h := range c.Hosts {
		quoted = append(quoted, tomlString(h))
	}
	return render("cassandra", cassandraTemplate, cassandraTmplData{
		Cassandra:    c,
		HostsLiteral: strings.Join(quoted, ", "),
	})
}
