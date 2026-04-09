package observability

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGrafanaInstallDashboard(t *testing.T) {
	var gotBody []byte
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/dashboards/db" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method %s", r.Method)
		}
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	defer srv.Close()

	client := NewGrafanaClient(srv.URL, "secret-token")
	if err := client.InstallDashboard(context.Background(), "prod"); err != nil {
		t.Fatalf("InstallDashboard: %v", err)
	}

	if gotAuth != "Bearer secret-token" {
		t.Errorf("auth header = %q", gotAuth)
	}

	var envelope map[string]interface{}
	if err := json.Unmarshal(gotBody, &envelope); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	dash, ok := envelope["dashboard"].(map[string]interface{})
	if !ok {
		t.Fatal("envelope missing dashboard field")
	}
	if _, ok := dash["id"]; ok {
		t.Error("dashboard id should be stripped on install")
	}
	title, _ := dash["title"].(string)
	if !strings.Contains(title, "prod") {
		t.Errorf("dashboard title should include cluster name suffix, got %q", title)
	}
}

func TestGrafanaInstallDashboardRequiresURL(t *testing.T) {
	c := NewGrafanaClient("", "t")
	if err := c.InstallDashboard(context.Background(), ""); err == nil {
		t.Error("expected error for empty base URL")
	}
}

func TestGrafanaInstallDashboardRequiresToken(t *testing.T) {
	c := NewGrafanaClient("http://x", "")
	if err := c.InstallDashboard(context.Background(), ""); err == nil {
		t.Error("expected error for empty token")
	}
}
