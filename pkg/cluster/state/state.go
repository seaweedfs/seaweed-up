// Package state provides on-disk persistence of SeaweedFS cluster
// topology and deployment metadata so CLI commands can resolve
// clusters by name without re-reading configuration files.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"gopkg.in/yaml.v3"
)

// EnvHome is the environment variable that overrides the default
// seaweed-up home directory used to store cluster state.
const EnvHome = "SEAWEED_UP_HOME"

// Meta describes deployment metadata for a persisted cluster.
type Meta struct {
	Name       string    `json:"name" yaml:"name"`
	Version    string    `json:"version" yaml:"version"`
	DeployedAt time.Time `json:"deployed_at" yaml:"deployed_at"`
	Hosts      []string  `json:"hosts" yaml:"hosts"`
}

// Entry is a single persisted cluster returned by List.
type Entry struct {
	Meta Meta
	Spec *spec.Specification
}

// Store is a filesystem-backed cluster state store.
type Store struct {
	dir string
}

// NewStore returns a Store rooted at the given directory. If dir is
// empty, the default directory is used (respecting SEAWEED_UP_HOME).
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		d, err := DefaultDir()
		if err != nil {
			return nil, err
		}
		dir = d
	}
	return &Store{dir: dir}, nil
}

// DefaultDir returns the default directory used to persist clusters.
// If SEAWEED_UP_HOME is set, clusters are stored under
// $SEAWEED_UP_HOME/clusters. Otherwise ~/.seaweed-up/clusters is used.
func DefaultDir() (string, error) {
	if v := os.Getenv(EnvHome); v != "" {
		return filepath.Join(v, "clusters"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(home, ".seaweed-up", "clusters"), nil
}

// Dir returns the root directory of the store.
func (s *Store) Dir() string {
	return s.dir
}

// clusterDir returns the directory for a specific cluster name.
func (s *Store) clusterDir(name string) string {
	return filepath.Join(s.dir, name)
}

// Save persists the cluster topology and metadata for the given name.
// If meta.Name is unset, it is populated from the name argument.
// If meta.Hosts is empty, hosts are derived from the specification.
// If meta.DeployedAt is zero, time.Now() is used.
func (s *Store) Save(name string, sp *spec.Specification, meta Meta) error {
	if name == "" {
		return fmt.Errorf("cluster name is required")
	}
	if sp == nil {
		return fmt.Errorf("cluster specification is required")
	}

	if meta.Name == "" {
		meta.Name = name
	}
	if meta.DeployedAt.IsZero() {
		meta.DeployedAt = time.Now().UTC()
	}
	if len(meta.Hosts) == 0 {
		meta.Hosts = HostsFromSpec(sp)
	}

	dir := s.clusterDir(name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create cluster dir %s: %w", dir, err)
	}

	topoPath := filepath.Join(dir, "topology.yaml")
	topoBytes, err := yaml.Marshal(sp)
	if err != nil {
		return fmt.Errorf("marshal topology: %w", err)
	}
	if err := writeFileAtomic(topoPath, topoBytes, 0o644); err != nil {
		return fmt.Errorf("write topology: %w", err)
	}

	statePath := filepath.Join(dir, "state.json")
	stateBytes, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	if err := writeFileAtomic(statePath, stateBytes, 0o644); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	return nil
}

// Load reads the persisted cluster by name.
func (s *Store) Load(name string) (*spec.Specification, Meta, error) {
	dir := s.clusterDir(name)
	topoPath := filepath.Join(dir, "topology.yaml")
	statePath := filepath.Join(dir, "state.json")

	topoBytes, err := os.ReadFile(topoPath)
	if err != nil {
		return nil, Meta{}, fmt.Errorf("read topology for cluster %q: %w", name, err)
	}
	sp := &spec.Specification{}
	if err := yaml.Unmarshal(topoBytes, sp); err != nil {
		return nil, Meta{}, fmt.Errorf("parse topology for cluster %q: %w", name, err)
	}

	var meta Meta
	stateBytes, err := os.ReadFile(statePath)
	if err != nil {
		return nil, Meta{}, fmt.Errorf("read state for cluster %q: %w", name, err)
	}
	if err := json.Unmarshal(stateBytes, &meta); err != nil {
		return nil, Meta{}, fmt.Errorf("parse state for cluster %q: %w", name, err)
	}
	if meta.Name == "" {
		meta.Name = name
	}
	return sp, meta, nil
}

// Exists reports whether a cluster with the given name is persisted.
func (s *Store) Exists(name string) bool {
	_, err := os.Stat(filepath.Join(s.clusterDir(name), "state.json"))
	return err == nil
}

// List returns all persisted clusters sorted by name. Clusters whose
// files are unreadable or malformed are skipped.
func (s *Store) List() []Entry {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil
	}
	var result []Entry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sp, meta, err := s.Load(e.Name())
		if err != nil {
			continue
		}
		result = append(result, Entry{Meta: meta, Spec: sp})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Meta.Name < result[j].Meta.Name
	})
	return result
}

// Delete removes all persisted state for the given cluster.
func (s *Store) Delete(name string) error {
	if name == "" {
		return fmt.Errorf("cluster name is required")
	}
	return os.RemoveAll(s.clusterDir(name))
}

// HostsFromSpec extracts a de-duplicated, sorted list of host IPs
// referenced by the specification across all component types.
func HostsFromSpec(sp *spec.Specification) []string {
	if sp == nil {
		return nil
	}
	seen := map[string]struct{}{}
	add := func(ip string) {
		if ip == "" {
			return
		}
		seen[ip] = struct{}{}
	}
	for _, m := range sp.MasterServers {
		add(m.Ip)
	}
	for _, v := range sp.VolumeServers {
		add(v.Ip)
	}
	for _, f := range sp.FilerServers {
		add(f.Ip)
	}
	for _, e := range sp.EnvoyServers {
		add(e.Ip)
	}
	hosts := make([]string, 0, len(seen))
	for h := range seen {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	return hosts
}

// writeFileAtomic writes data to path via a temp file + rename so that
// readers never observe a partially-written file.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
