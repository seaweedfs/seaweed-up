// Package health provides HTTP-based health probing for SeaweedFS cluster components.
package health

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sync"
	"time"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

// serverHeaderVersionRe matches "SeaweedFS <version>" in an HTTP Server header,
// e.g. "SeaweedFS 3.75". The filer root response carries this header but has
// no JSON body, so it is our only version signal.
var serverHeaderVersionRe = regexp.MustCompile(`SeaweedFS[\s/]+([0-9][0-9A-Za-z._+\-]*)`)

// parseServerHeaderVersion extracts a SeaweedFS version from a Server header
// value. Returns "" when the header is absent or does not match.
func parseServerHeaderVersion(h string) string {
	if h == "" {
		return ""
	}
	m := serverHeaderVersionRe.FindStringSubmatch(h)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// ComponentKind identifies the kind of SeaweedFS component probed.
type ComponentKind string

const (
	KindMaster ComponentKind = "master"
	KindVolume ComponentKind = "volume"
	KindFiler  ComponentKind = "filer"
)

// maxResponseBytes caps the amount of data we will read from a probed
// component's HTTP response. The SeaweedFS master /cluster/status and
// /dir/status endpoints can return sizable topology documents on large
// clusters (many volume servers, thousands of volumes), so we pick a
// generous 10 MiB ceiling: large enough for realistic production clusters
// while still bounding memory usage from a single misbehaving endpoint.
const maxResponseBytes int64 = 10 << 20

// ProbeResult is the result of probing a single component endpoint.
type ProbeResult struct {
	Kind    ComponentKind  `json:"kind"`
	Address string         `json:"address"`
	Healthy bool           `json:"healthy"`
	Version string         `json:"version,omitempty"`
	Raw     map[string]any `json:"raw,omitempty"`
	Err     string         `json:"error,omitempty"`
	// Extra holds supplemental probes (e.g. /dir/status for masters).
	Extra map[string]map[string]any `json:"extra,omitempty"`
}

// ClusterHealth aggregates probe results for a cluster.
type ClusterHealth struct {
	Masters []ProbeResult `json:"masters"`
	Volumes []ProbeResult `json:"volumes"`
	Filers  []ProbeResult `json:"filers"`
}

// AllHealthy returns true when every probed component is healthy.
func (c *ClusterHealth) AllHealthy() bool {
	for _, r := range c.Masters {
		if !r.Healthy {
			return false
		}
	}
	for _, r := range c.Volumes {
		if !r.Healthy {
			return false
		}
	}
	for _, r := range c.Filers {
		if !r.Healthy {
			return false
		}
	}
	return true
}

// UnhealthyCount returns the total number of unhealthy components across
// masters, volumes, and filers.
func (c *ClusterHealth) UnhealthyCount() int {
	n := 0
	for _, r := range c.Masters {
		if !r.Healthy {
			n++
		}
	}
	for _, r := range c.Volumes {
		if !r.Healthy {
			n++
		}
	}
	for _, r := range c.Filers {
		if !r.Healthy {
			n++
		}
	}
	return n
}

// Prober issues HTTP probes with a shared timeout and http.Client.
type Prober struct {
	Timeout time.Duration
	Client  *http.Client
	// Scheme is the URL scheme used when constructing probe URLs, either
	// "http" or "https". Empty is treated as "http".
	Scheme string
}

// NewProber returns a Prober with sensible defaults that speaks HTTP.
// Use NewProberForSpec to derive scheme and TLS settings from a cluster
// specification.
func NewProber(timeout time.Duration) *Prober {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Prober{
		Timeout: timeout,
		Scheme:  "http",
		Client: &http.Client{
			Timeout: timeout,
		},
	}
}

// NewProberForSpec builds a Prober whose URL scheme and TLS trust roots
// match the cluster spec. When the spec has TLS enabled, the returned
// Prober speaks HTTPS and trusts the cluster CA found under
// ~/.seaweed-up/clusters/<name>/certs/ca.crt when present; otherwise it
// falls back to the system trust store. InsecureSkipVerify is never set.
//
// clusterCertDir is the directory where the cluster CA lives (typically
// the same directory returned by pkg/cluster/tls.LocalClusterDir). Pass
// an empty string to skip CA loading and use the system roots.
func NewProberForSpec(timeout time.Duration, s *spec.Specification, clusterCertDir string) *Prober {
	p := NewProber(timeout)
	if s == nil || !s.GlobalOptions.TLSEnabled {
		return p
	}
	p.Scheme = "https"

	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if clusterCertDir != "" {
		caPath := clusterCertDir + "/ca.crt"
		if pem, err := os.ReadFile(caPath); err == nil && len(pem) > 0 {
			pool := x509.NewCertPool()
			if pool.AppendCertsFromPEM(pem) {
				tlsCfg.RootCAs = pool
			}
		}
	}
	p.Client = &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
	}
	return p
}

// scheme returns the URL scheme to use for probe URLs.
func (p *Prober) scheme() string {
	if p.Scheme == "" {
		return "http"
	}
	return p.Scheme
}

// fetchJSON GETs the URL and decodes the response body as a JSON object.
func (p *Prober) fetchJSON(ctx context.Context, url string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxResponseBytes {
		return nil, fmt.Errorf("response body exceeded %d bytes limit", maxResponseBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d: %s (truncated)", resp.StatusCode, string(body[:min(len(body), 1024)]))
	}
	var out map[string]any
	if len(body) == 0 {
		return map[string]any{}, nil
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return out, nil
}

// extractVersion hunts for common version keys in a SeaweedFS response.
func extractVersion(m map[string]any) string {
	if m == nil {
		return ""
	}
	for _, k := range []string{"Version", "version"} {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	// Some endpoints nest version under a sub-object.
	for _, k := range []string{"Topology", "topology"} {
		if sub, ok := m[k].(map[string]any); ok {
			if v := extractVersion(sub); v != "" {
				return v
			}
		}
	}
	return ""
}

// ProbeMaster probes master /cluster/status and /dir/status.
func (p *Prober) ProbeMaster(ctx context.Context, ip string, port int) ProbeResult {
	addr := fmt.Sprintf("%s:%d", ip, port)
	res := ProbeResult{Kind: KindMaster, Address: addr, Extra: map[string]map[string]any{}}

	clusterURL := fmt.Sprintf("%s://%s/cluster/status", p.scheme(), addr)
	data, err := p.fetchJSON(ctx, clusterURL)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	res.Raw = data
	res.Version = extractVersion(data)

	dirURL := fmt.Sprintf("%s://%s/dir/status", p.scheme(), addr)
	if dirData, derr := p.fetchJSON(ctx, dirURL); derr == nil {
		res.Extra["dir_status"] = dirData
		if res.Version == "" {
			res.Version = extractVersion(dirData)
		}
	} else {
		res.Err = fmt.Sprintf("dir/status: %v", derr)
		return res
	}

	res.Healthy = true
	return res
}

// ProbeVolume probes volume /status.
func (p *Prober) ProbeVolume(ctx context.Context, ip string, port int) ProbeResult {
	addr := fmt.Sprintf("%s:%d", ip, port)
	res := ProbeResult{Kind: KindVolume, Address: addr}
	data, err := p.fetchJSON(ctx, fmt.Sprintf("%s://%s/status", p.scheme(), addr))
	if err != nil {
		res.Err = err.Error()
		return res
	}
	res.Raw = data
	res.Version = extractVersion(data)
	res.Healthy = true
	return res
}

// ProbeFiler probes the filer root endpoint. The SeaweedFS filer has no
// /status endpoint; instead we GET / (which returns a directory listing) and
// treat any 2xx as healthy. Version, when available, is parsed from the
// "Server" response header (e.g. "SeaweedFS 3.75").
func (p *Prober) ProbeFiler(ctx context.Context, ip string, port int) ProbeResult {
	addr := fmt.Sprintf("%s:%d", ip, port)
	res := ProbeResult{Kind: KindFiler, Address: addr}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s://%s/", p.scheme(), addr), nil)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	resp, err := p.Client.Do(req)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	defer resp.Body.Close()
	// Drain (bounded) so the connection can be reused, but we don't require
	// or parse the body — the filer root returns HTML/JSON directory listings
	// that aren't relevant to health.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		res.Err = fmt.Sprintf("status %d", resp.StatusCode)
		return res
	}
	res.Version = parseServerHeaderVersion(resp.Header.Get("Server"))
	res.Healthy = true
	return res
}

// Probe runs probes for every component in the cluster spec in parallel.
func (p *Prober) Probe(ctx context.Context, s *spec.Specification) *ClusterHealth {
	h := &ClusterHealth{
		Masters: make([]ProbeResult, len(s.MasterServers)),
		Volumes: make([]ProbeResult, len(s.VolumeServers)),
		Filers:  make([]ProbeResult, len(s.FilerServers)),
	}

	var wg sync.WaitGroup
	for i, m := range s.MasterServers {
		wg.Add(1)
		go func(i int, m *spec.MasterServerSpec) {
			defer wg.Done()
			port := m.Port
			if port == 0 {
				port = 9333
			}
			h.Masters[i] = p.ProbeMaster(ctx, m.Ip, port)
		}(i, m)
	}
	for i, v := range s.VolumeServers {
		wg.Add(1)
		go func(i int, v *spec.VolumeServerSpec) {
			defer wg.Done()
			port := v.Port
			if port == 0 {
				port = 8080
			}
			h.Volumes[i] = p.ProbeVolume(ctx, v.Ip, port)
		}(i, v)
	}
	for i, f := range s.FilerServers {
		wg.Add(1)
		go func(i int, f *spec.FilerServerSpec) {
			defer wg.Done()
			port := f.Port
			if port == 0 {
				port = 8888
			}
			h.Filers[i] = p.ProbeFiler(ctx, f.Ip, port)
		}(i, f)
	}
	wg.Wait()
	return h
}
