package observability

import (
	"encoding/base64"
	"io"
	"strings"
	"testing"
)

// decodedFiles returns the concatenated decoded content of every
// `echo <b64> | base64 -d > path` snippet in a script, so tests can assert
// on the config files the script writes (their content is base64-embedded).
func decodedFiles(script string) string {
	var out strings.Builder
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(line, "echo ") && strings.Contains(line, "| base64 -d > ") {
			b64 := strings.TrimSpace(strings.TrimPrefix(strings.SplitN(line, "|", 2)[0], "echo "))
			if dec, err := base64.StdEncoding.DecodeString(b64); err == nil {
				out.Write(dec)
				out.WriteByte('\n')
			}
		}
	}
	return out.String()
}

// recordOp records executed commands without opening any SSH session.
type recordOp struct{ executed []string }

func (r *recordOp) Execute(cmd string) error               { r.executed = append(r.executed, cmd); return nil }
func (r *recordOp) Output(cmd string) ([]byte, error)      { r.executed = append(r.executed, cmd); return []byte("x86_64\n"), nil }
func (r *recordOp) Upload(io.Reader, string, string) error { return nil }
func (r *recordOp) UploadFile(string, string, string) error {
	return nil
}

func TestRunScriptSudoVariants(t *testing.T) {
	cases := []struct {
		name, user, pass string
		wantContains     string
		wantNotContains  string
	}{
		{"root runs bare", "root", "", "| base64 -d | sh", "sudo"},
		{"non-root passwordless uses sudo -n", "chris", "", "| base64 -d | sudo -n sh", "sudo -S"},
		{"non-root with password uses sudo -S", "chris", "pw", "sudo -S -p '' sh", "sudo -n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			op := &recordOp{}
			if err := runScript(op, tc.user, tc.pass, "echo hi"); err != nil {
				t.Fatal(err)
			}
			got := strings.Join(op.executed, "\n")
			if !strings.Contains(got, tc.wantContains) {
				t.Errorf("want %q in %q", tc.wantContains, got)
			}
			if tc.wantNotContains != "" && strings.Contains(got, tc.wantNotContains) {
				t.Errorf("did not want %q in %q", tc.wantNotContains, got)
			}
		})
	}
}

func TestPrometheusInstallScript(t *testing.T) {
	s := PrometheusInstallScript("amd64", PrometheusOptions{
		ConfigYAML: "scrape_configs: []\n", Bind: "127.0.0.1", Port: 9090, Retention: "30d",
	})
	// structural parts are literal in the script
	for _, want := range []string{
		"prometheus-" + PrometheusVersion + ".linux-amd64",
		"/usr/local/bin/prometheus",
		"promtool check config",
		"systemctl restart prometheus.service",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("prometheus script missing %q", want)
		}
	}
	// config/unit content is base64-embedded; decode to assert
	files := decodedFiles(s)
	for _, want := range []string{
		"--web.listen-address=127.0.0.1:9090",
		"--storage.tsdb.retention.time=30d",
		"scrape_configs: []",
	} {
		if !strings.Contains(files, want) {
			t.Errorf("prometheus embedded files missing %q", want)
		}
	}
}

func TestGrafanaInstallScript(t *testing.T) {
	s := GrafanaInstallScript("amd64", []byte(`{"title":"SeaweedFS"}`), GrafanaOptions{
		Bind: "0.0.0.0", Port: 3000, AdminUser: "admin", AdminPassword: "secret",
		PrometheusURL: "http://127.0.0.1:9090", ClusterName: "prod",
	})
	for _, want := range []string{
		"grafana-" + GrafanaVersion + ".linux-amd64",
		"/var/lib/grafana/dashboards/seaweedfs.json",
		"systemctl restart grafana.service",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("grafana script missing %q", want)
		}
	}
	files := decodedFiles(s)
	for _, want := range []string{
		"/opt/grafana/bin/grafana server",
		"http_addr = 0.0.0.0",
		"admin_password = secret",
		"uid: seaweedprom",
		"http://127.0.0.1:9090",
		"SeaweedFS - prod",
	} {
		if !strings.Contains(files, want) {
			t.Errorf("grafana embedded files missing %q", want)
		}
	}
}

func TestPrepareDashboardSuffixesTitleAndDropsID(t *testing.T) {
	out := prepareDashboard([]byte(`{"id":7,"title":"SeaweedFS"}`), "prod")
	got := string(out)
	if strings.Contains(got, `"id"`) {
		t.Errorf("expected id removed: %s", got)
	}
	if !strings.Contains(got, "SeaweedFS - prod") {
		t.Errorf("expected title suffix: %s", got)
	}
}
