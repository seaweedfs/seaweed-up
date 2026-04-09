// Package scale implements cluster scale-in operations for SeaweedFS,
// including draining data away from a target volume server before removal.
package scale

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HTTPClient is the subset of http.Client that Drain uses. It is defined as
// an interface so tests can supply a custom transport / client.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// defaultClient is used when no client is injected.
var defaultClient HTTPClient = &http.Client{Timeout: 30 * time.Second}

// pollInterval controls how frequently /dir/status is polled. Exposed as a
// package variable so tests can override it.
var pollInterval = 2 * time.Second

// Drain marks the given target volume server read-only on the master and
// asks the master to evacuate all volumes off the node. It polls the master
// until the target reports 0 volumes or the timeout expires.
//
// masterAddr is expected in the form "host:port" (no scheme). targetNode is
// the volume server address as seen by the master, typically "ip:port".
func Drain(masterAddr, targetNode string, timeout time.Duration) error {
	return DrainWithClient(defaultClient, masterAddr, targetNode, timeout)
}

// DrainWithClient is like Drain but uses the supplied HTTP client. This is
// primarily intended for testing against httptest servers.
func DrainWithClient(client HTTPClient, masterAddr, targetNode string, timeout time.Duration) error {
	if client == nil {
		client = defaultClient
	}
	if masterAddr == "" {
		return fmt.Errorf("drain: master address is required")
	}
	if targetNode == "" {
		return fmt.Errorf("drain: target node is required")
	}

	base := normalizeMaster(masterAddr)

	// Best-effort mark read-only. A non-2xx is logged-equivalent but does
	// not abort; the evacuate call is the authoritative step.
	if err := postNode(client, base+"/cluster/volumeServer.markReadonly", targetNode); err != nil {
		// mark read-only may not be supported on all versions; continue.
		_ = err
	}

	// Kick off evacuation. If this fails outright, return the error so the
	// caller can fall back to the `weed shell` path over SSH.
	if err := postNode(client, base+"/cluster/evacuate", targetNode); err != nil {
		return fmt.Errorf("drain: evacuate request failed: %w", err)
	}

	deadline := time.Now().Add(timeout)
	for {
		count, err := targetVolumeCount(client, base, targetNode)
		if err == nil && count == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("drain: timed out waiting for %s (last error: %v)", targetNode, err)
			}
			return fmt.Errorf("drain: timed out waiting for %s to reach 0 volumes (still %d)", targetNode, count)
		}
		time.Sleep(pollInterval)
	}
}

// normalizeMaster makes sure the master address has an http scheme prefix.
func normalizeMaster(masterAddr string) string {
	if strings.HasPrefix(masterAddr, "http://") || strings.HasPrefix(masterAddr, "https://") {
		return strings.TrimRight(masterAddr, "/")
	}
	return "http://" + strings.TrimRight(masterAddr, "/")
}

func postNode(client HTTPClient, endpoint, node string) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	q := u.Query()
	q.Set("node", node)
	u.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodPost, u.String(), nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read drain response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("POST %s: status %d: %s", endpoint, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// targetVolumeCount fetches /dir/status and returns the number of volumes
// still hosted on the target node. This is a best-effort parse against
// SeaweedFS's topology JSON (structure: Topology.DataCenters[].Racks[].DataNodes[]).
func targetVolumeCount(client HTTPClient, base, targetNode string) (int, error) {
	req, err := http.NewRequest(http.MethodGet, base+"/dir/status", nil)
	if err != nil {
		return -1, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return -1, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return -1, fmt.Errorf("GET /dir/status: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var status dirStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return -1, fmt.Errorf("decode /dir/status: %w", err)
	}

	target := normalizeTarget(targetNode)
	for _, dc := range status.Topology.DataCenters {
		for _, rack := range dc.Racks {
			for _, dn := range rack.DataNodes {
				if matchesNode(dn, target) {
					return dn.Volumes, nil
				}
			}
		}
	}
	// Not found means the master no longer tracks the node - treat as drained.
	return 0, nil
}

func normalizeTarget(t string) string {
	return strings.TrimSpace(t)
}

func matchesNode(dn dataNode, target string) bool {
	if dn.URL == target || dn.PublicURL == target {
		return true
	}
	// Some versions use "Url" fields without scheme; others include one.
	// Strip both http:// and https:// prefixes when comparing.
	if stripScheme(dn.URL) == target || stripScheme(dn.PublicURL) == target {
		return true
	}
	return false
}

// stripScheme removes http:// or https:// prefix from the given address.
func stripScheme(addr string) string {
	addr = strings.TrimPrefix(addr, "https://")
	addr = strings.TrimPrefix(addr, "http://")
	return addr
}

// dirStatus matches the pieces of /dir/status that Drain consumes. Extra
// fields are ignored.
type dirStatus struct {
	Topology topology `json:"Topology"`
}

type topology struct {
	DataCenters []dataCenter `json:"DataCenters"`
}

type dataCenter struct {
	Racks []rack `json:"Racks"`
}

type rack struct {
	DataNodes []dataNode `json:"DataNodes"`
}

type dataNode struct {
	URL       string `json:"Url"`
	PublicURL string `json:"PublicUrl"`
	Volumes   int    `json:"Volumes"`
}
