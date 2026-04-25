package plan

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ParseFilerBackendDSN turns a URL-style DSN into a filer Config map
// compatible with FilerServerSpec.Config (the same shape consumed by
// filer.FromConfig). The supported schemes are documented in
// docs/design/inventory-and-plan.md:
//
//	postgres://user:pass@host:port/db?sslmode=disable
//	mysql://user:pass@host:port/db
//	redis://[:pass@]host:port/[db]
//
// Aliases accepted: postgres/postgresql/postgres2 → postgres; mysql/
// mysql2 → mysql; redis/redis2 → redis2.
//
// The returned map is safe to assign to FilerServerSpec.Config directly.
// Unknown schemes return an error listing the supported set so the CLI
// can surface a clear message.
func ParseFilerBackendDSN(dsn string) (map[string]interface{}, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, fmt.Errorf("filer backend DSN is empty")
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse filer backend DSN: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)

	switch scheme {
	case "postgres", "postgresql", "postgres2":
		return postgresFromURL(u)
	case "mysql", "mysql2":
		return mysqlFromURL(u)
	case "redis", "redis2":
		return redisFromURL(u)
	default:
		return nil, fmt.Errorf("unsupported filer backend scheme %q: supported schemes are postgres, mysql, redis", u.Scheme)
	}
}

func postgresFromURL(u *url.URL) (map[string]interface{}, error) {
	host, port, err := hostPort(u, 5432)
	if err != nil {
		return nil, err
	}
	database := strings.TrimPrefix(u.Path, "/")
	if database == "" {
		// filer.Postgres.Validate requires database; reject early so
		// plan doesn't write a cluster.yaml deploy will turn around
		// and refuse.
		return nil, fmt.Errorf("postgres DSN missing database (e.g. postgres://user@host/dbname)")
	}
	user := u.User
	if user == nil || user.Username() == "" {
		return nil, fmt.Errorf("postgres DSN missing username (filer.Postgres.Validate requires it)")
	}
	out := map[string]interface{}{
		"type":     "postgres",
		"hostname": host,
		"port":     port,
		"database": database,
		"username": user.Username(),
	}
	if pw, set := user.Password(); set {
		out["password"] = pw
	}
	// sslmode is a postgres-specific conventional query param; forward it
	// through when present. Anything else is ignored — operators can
	// hand-edit after plan if they need more knobs.
	if v := u.Query().Get("sslmode"); v != "" {
		out["sslmode"] = v
	}
	return out, nil
}

func mysqlFromURL(u *url.URL) (map[string]interface{}, error) {
	host, port, err := hostPort(u, 3306)
	if err != nil {
		return nil, err
	}
	database := strings.TrimPrefix(u.Path, "/")
	if database == "" {
		return nil, fmt.Errorf("mysql DSN missing database (e.g. mysql://user@host/dbname)")
	}
	user := u.User
	if user == nil || user.Username() == "" {
		return nil, fmt.Errorf("mysql DSN missing username (filer.MySQL.Validate requires it)")
	}
	out := map[string]interface{}{
		"type":     "mysql",
		"hostname": host,
		"port":     port,
		"database": database,
		"username": user.Username(),
	}
	if pw, set := user.Password(); set {
		out["password"] = pw
	}
	return out, nil
}

func redisFromURL(u *url.URL) (map[string]interface{}, error) {
	host, port, err := hostPort(u, 6379)
	if err != nil {
		return nil, err
	}
	// net.JoinHostPort handles IPv6 literals by wrapping them in
	// brackets so downstream Redis clients can parse "[::1]:6379".
	out := map[string]interface{}{
		"type":    "redis2",
		"address": net.JoinHostPort(host, strconv.Itoa(port)),
	}
	// Redis URLs carry the password in the user-info field under the
	// empty username ("redis://:pass@host").
	if user := u.User; user != nil {
		if pw, set := user.Password(); set {
			out["password"] = pw
		}
	}
	if p := strings.TrimPrefix(u.Path, "/"); p != "" {
		db, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("redis DSN: database path must be an integer, got %q", p)
		}
		out["database"] = db
	}
	return out, nil
}

// hostPort splits u.Host into (host, port), falling back to defaultPort
// when the URL omits it. Returns a sensible error for DSNs that forgot
// the host altogether.
func hostPort(u *url.URL, defaultPort int) (string, int, error) {
	host := u.Hostname()
	if host == "" {
		return "", 0, fmt.Errorf("filer backend DSN missing host")
	}
	portStr := u.Port()
	if portStr == "" {
		return host, defaultPort, nil
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, fmt.Errorf("filer backend DSN: invalid port %q", portStr)
	}
	if port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("filer backend DSN: port %d out of range (1-65535)", port)
	}
	return host, port, nil
}
