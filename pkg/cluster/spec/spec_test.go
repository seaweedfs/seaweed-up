package spec

import "testing"

func masterOnly() []*MasterServerSpec {
	return []*MasterServerSpec{{Ip: "10.0.0.1", Port: 9333}}
}

func TestValidate_Bastion(t *testing.T) {
	cases := []struct {
		name    string
		bastion *BastionSpec
		wantErr bool
	}{
		{"no bastion", nil, false},
		{"valid host+port", &BastionSpec{Host: "192.71.171.132", Port: 22}, false},
		{"port unset is allowed (defaults to 22)", &BastionSpec{Host: "bastion"}, false},
		{"blank host", &BastionSpec{Host: "   "}, true},
		{"empty host", &BastionSpec{Host: "", Port: 22}, true},
		{"negative port", &BastionSpec{Host: "bastion", Port: -1}, true},
		{"port too large", &BastionSpec{Host: "bastion", Port: 70000}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Specification{MasterServers: masterOnly()}
			s.GlobalOptions.Bastion = tc.bastion
			err := s.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected validation error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func TestValidate_SSHHostKeyCheck(t *testing.T) {
	cases := []struct {
		val     string
		wantErr bool
	}{
		{"", false},
		{"ignore", false},
		{"accept-new", false},
		{"strict", false},
		{"STRICT", true},
		{"tofu", true},
	}
	for _, tc := range cases {
		t.Run(tc.val, func(t *testing.T) {
			s := &Specification{MasterServers: masterOnly()}
			s.GlobalOptions.SSHHostKeyCheck = tc.val
			err := s.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for %q, got nil", tc.val)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected no error for %q, got %v", tc.val, err)
			}
		})
	}
}

func TestValidate_Monitoring(t *testing.T) {
	cases := []struct {
		name    string
		mon     *MonitoringSpec
		wantErr bool
	}{
		{"nil ok", nil, false},
		{"host + password ok", &MonitoringSpec{Host: "10.0.0.1", GrafanaAdminPassword: "s3cret"}, false},
		{"blank host", &MonitoringSpec{Host: "  ", GrafanaAdminPassword: "s3cret"}, true},
		{"missing grafana password", &MonitoringSpec{Host: "10.0.0.1"}, true},
		{"blank grafana password", &MonitoringSpec{Host: "10.0.0.1", GrafanaAdminPassword: "  "}, true},
		{"bad grafana port", &MonitoringSpec{Host: "h", GrafanaAdminPassword: "s3cret", GrafanaPort: 70000}, true},
		{"bad prometheus port", &MonitoringSpec{Host: "h", GrafanaAdminPassword: "s3cret", PrometheusPort: -1}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Specification{MasterServers: masterOnly()}
			s.Monitoring = tc.mon
			err := s.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func TestMonitoringDefaults(t *testing.T) {
	m := &MonitoringSpec{Host: "10.0.0.1"}
	if m.EffectiveBind() != "127.0.0.1" {
		t.Errorf("bind default = %q", m.EffectiveBind())
	}
	if m.EffectivePrometheusPort() != 9090 || m.EffectiveGrafanaPort() != 3000 {
		t.Errorf("port defaults wrong: %d %d", m.EffectivePrometheusPort(), m.EffectiveGrafanaPort())
	}
	if m.EffectiveGrafanaAdminUser() != "admin" || m.EffectiveRetention() != "15d" {
		t.Errorf("defaults wrong: %q %q", m.EffectiveGrafanaAdminUser(), m.EffectiveRetention())
	}
	if !m.InstallNodeExporter() {
		t.Errorf("node_exporter should default true")
	}
	no := false
	m.NodeExporter = &no
	if m.InstallNodeExporter() {
		t.Errorf("node_exporter explicit false not honored")
	}
}
