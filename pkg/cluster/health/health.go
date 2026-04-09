// Package health provides HTTP-based health probing for SeaweedFS cluster components.
package health

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

// ComponentKind identifies the kind of SeaweedFS component probed.
type ComponentKind string

const (
	KindMaster ComponentKind = "master"
	KindVolume ComponentKind = "volume"
	KindFiler  ComponentKind = "filer"
)

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

// Prober issues HTTP probes with a shared timeout and http.Client.
type Prober struct {
	Timeout time.Duration
	Client  *http.Client
}

// NewProber returns a Prober with sensible defaults.
func NewProber(timeout time.Duration) *Prober {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Prober{
		Timeout: timeout,
		Client: &http.Client{
			Timeout: timeout,
		},
	}
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, string(body))
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

	clusterURL := fmt.Sprintf("http://%s/cluster/status", addr)
	data, err := p.fetchJSON(ctx, clusterURL)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	res.Raw = data
	res.Version = extractVersion(data)

	dirURL := fmt.Sprintf("http://%s/dir/status", addr)
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
	data, err := p.fetchJSON(ctx, fmt.Sprintf("http://%s/status", addr))
	if err != nil {
		res.Err = err.Error()
		return res
	}
	res.Raw = data
	res.Version = extractVersion(data)
	res.Healthy = true
	return res
}

// ProbeFiler probes filer /status.
func (p *Prober) ProbeFiler(ctx context.Context, ip string, port int) ProbeResult {
	addr := fmt.Sprintf("%s:%d", ip, port)
	res := ProbeResult{Kind: KindFiler, Address: addr}
	data, err := p.fetchJSON(ctx, fmt.Sprintf("http://%s/status", addr))
	if err != nil {
		res.Err = err.Error()
		return res
	}
	res.Raw = data
	res.Version = extractVersion(data)
	res.Healthy = true
	return res
}

// Probe runs probes for every component in the cluster spec in parallel.
func (p *Prober) Probe(ctx context.Context, s *spec.Specification) *ClusterHealth {
	health := &ClusterHealth{
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
			health.Masters[i] = p.ProbeMaster(ctx, m.Ip, port)
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
			health.Volumes[i] = p.ProbeVolume(ctx, v.Ip, port)
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
			health.Filers[i] = p.ProbeFiler(ctx, f.Ip, port)
		}(i, f)
	}
	wg.Wait()
	return health
}
