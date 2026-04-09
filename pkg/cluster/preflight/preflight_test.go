package preflight

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

func TestParseListeningPorts(t *testing.T) {
	out := `State      Recv-Q Send-Q Local Address:Port  Peer Address:Port
LISTEN     0      128         *:22               *:*
LISTEN     0      128    127.0.0.1:9333             *:*
LISTEN     0      128    [::1]:8080                [::]:*
LISTEN     0      128    0.0.0.0:19333              *:*
`
	got := ParseListeningPorts(out)
	want := map[int]bool{22: true, 9333: true, 8080: true, 19333: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseListeningPorts mismatch:\ngot  %v\nwant %v", got, want)
	}
}

func TestParseListeningPortsEmpty(t *testing.T) {
	got := ParseListeningPorts("")
	if len(got) != 0 {
		t.Fatalf("expected no ports, got %v", got)
	}
}

func TestParseFreeKB(t *testing.T) {
	out := `Filesystem     1024-blocks      Used Available Capacity Mounted on
/dev/sda1        103081248  50000000  53081248      49% /
`
	got, err := ParseFreeKB(out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 53081248 {
		t.Fatalf("got %d, want 53081248", got)
	}
}

func TestParseFreeKBError(t *testing.T) {
	if _, err := ParseFreeKB("just a header"); err == nil {
		t.Fatal("expected error on short output")
	}
}

func TestArchEqual(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"amd64", "x86_64", true},
		{"x86_64", "amd64", true},
		{"arm64", "aarch64", true},
		{"amd64", "arm64", false},
		{"", "x86_64", false},
	}
	for _, tc := range cases {
		if got := archEqual(tc.a, tc.b); got != tc.want {
			t.Errorf("archEqual(%q,%q)=%v want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestBuildHostPlans(t *testing.T) {
	s := &spec.Specification{
		GlobalOptions: spec.GlobalOptions{DataDir: "/data", OS: "linux"},
		MasterServers: []*spec.MasterServerSpec{
			{Ip: "10.0.0.1", Port: 9333, PortGrpc: 19333},
		},
		VolumeServers: []*spec.VolumeServerSpec{
			{Ip: "10.0.0.2", Port: 8080, PortGrpc: 18080},
		},
		FilerServers: []*spec.FilerServerSpec{
			{Ip: "10.0.0.1", Port: 8888, PortGrpc: 18888, S3Port: 8333},
		},
		EnvoyServers: []*spec.EnvoyServerSpec{
			{Ip: "10.0.0.3"},
		},
	}
	plans := BuildHostPlans(s)
	if len(plans) != 3 {
		t.Fatalf("want 3 plans, got %d", len(plans))
	}
	m := map[string]HostPlan{}
	for _, p := range plans {
		m[p.Host] = p
	}
	h1 := m["10.0.0.1"]
	wantPorts := []int{8333, 8888, 9333, 18888, 19333}
	sort.Ints(h1.Ports)
	if !reflect.DeepEqual(h1.Ports, wantPorts) {
		t.Errorf("host1 ports=%v want %v", h1.Ports, wantPorts)
	}
	if !reflect.DeepEqual(h1.Components, []string{"filer", "master"}) {
		t.Errorf("host1 components=%v", h1.Components)
	}
	if h1.DataDir != "/data" {
		t.Errorf("host1 data dir=%s", h1.DataDir)
	}

	h3 := m["10.0.0.3"]
	if !reflect.DeepEqual(h3.Ports, []int{8001}) {
		t.Errorf("envoy ports=%v", h3.Ports)
	}
}

// fakeRunner captures commands and returns scripted responses.
type fakeRunner struct {
	responses map[string]string
	errs      map[string]error
	calls     []string
}

func (f *fakeRunner) Output(cmd string) ([]byte, error) {
	f.calls = append(f.calls, cmd)
	if err, ok := f.errs[cmd]; ok {
		return nil, err
	}
	if r, ok := f.responses[cmd]; ok {
		return []byte(r), nil
	}
	// Fallback: try prefix matching for convenience.
	for k, v := range f.responses {
		if len(cmd) >= len(k) && cmd[:len(k)] == k {
			return []byte(v), nil
		}
	}
	return nil, nil
}

func TestPortsFreeCheck(t *testing.T) {
	plan := HostPlan{Host: "h1", Ports: []int{9333, 19333}}
	r := &fakeRunner{responses: map[string]string{
		"ss -tln 2>/dev/null || netstat -tln 2>/dev/null": `State Recv-Q Send-Q Local Address:Port Peer
LISTEN 0 128 0.0.0.0:9333 *:*
`,
	}}
	res := portsFreeCheck{}.Run(context.Background(), plan, r)
	if res.OK {
		t.Fatalf("expected FAIL, got %+v", res)
	}
	if !contains(res.Detail, "9333") {
		t.Errorf("detail should mention 9333: %s", res.Detail)
	}
}

func TestPortsFreeCheckAllFree(t *testing.T) {
	plan := HostPlan{Host: "h1", Ports: []int{9333}}
	r := &fakeRunner{responses: map[string]string{
		"ss -tln 2>/dev/null || netstat -tln 2>/dev/null": ``,
	}}
	res := portsFreeCheck{}.Run(context.Background(), plan, r)
	if !res.OK {
		t.Fatalf("expected OK: %+v", res)
	}
}

func TestHasFailure(t *testing.T) {
	if HasFailure([]Result{{OK: true}, {OK: true, Warn: true}}) {
		t.Error("should not have failures")
	}
	if !HasFailure([]Result{{OK: true}, {OK: false}}) {
		t.Error("expected failure")
	}
	if HasFailure([]Result{{OK: false, Warn: true}}) {
		t.Error("warn-only should not be a failure")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
