package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GrafanaClient pushes the bundled dashboard JSON to a Grafana API.
type GrafanaClient struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// NewGrafanaClient builds a Grafana client with a sensible default HTTP timeout.
func NewGrafanaClient(baseURL, token string) *GrafanaClient {
	return &GrafanaClient{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// InstallDashboard uploads the bundled dashboard to Grafana via
// /api/dashboards/db. The clusterName, if non-empty, is injected as the
// dashboard title suffix so multiple clusters can coexist.
func (g *GrafanaClient) InstallDashboard(ctx context.Context, clusterName string) error {
	if g.BaseURL == "" {
		return fmt.Errorf("grafana: base URL is required")
	}
	if g.Token == "" {
		return fmt.Errorf("grafana: API token is required")
	}

	payload, err := buildDashboardPayload(DashboardJSON(), clusterName)
	if err != nil {
		return err
	}

	url := g.BaseURL + "/api/dashboards/db"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("grafana: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+g.Token)
	req.Header.Set("Content-Type", "application/json")

	client := g.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("grafana: POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("grafana: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// buildDashboardPayload wraps the raw dashboard JSON with the envelope
// expected by Grafana's /api/dashboards/db endpoint.
func buildDashboardPayload(raw []byte, clusterName string) ([]byte, error) {
	var dash map[string]interface{}
	if err := json.Unmarshal(raw, &dash); err != nil {
		return nil, fmt.Errorf("grafana: parse bundled dashboard: %w", err)
	}
	// A fresh install must omit id so Grafana assigns one.
	delete(dash, "id")
	if clusterName != "" {
		if title, ok := dash["title"].(string); ok && title != "" {
			dash["title"] = title + " - " + clusterName
		} else {
			dash["title"] = "SeaweedFS - " + clusterName
		}
	}

	envelope := map[string]interface{}{
		"dashboard": dash,
		"overwrite": true,
		"message":   "Installed by seaweed-up",
	}
	return json.Marshal(envelope)
}
