package manager

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

func workerInstallScripts(t *testing.T, op *fakeOperator) (goUnit, lanceUnit bool) {
	t.Helper()
	for path := range op.uploads {
		if strings.HasSuffix(path, "/install_worker0.sh") {
			goUnit = true
		}
		if strings.HasSuffix(path, "/install_worker-lance0.sh") {
			lanceUnit = true
		}
	}
	return
}

// stubLanceAssetHead swaps the release-asset probe for the test's duration.
func stubLanceAssetHead(t *testing.T, fn func(url string) (int, error)) {
	t.Helper()
	old := lanceAssetHead
	lanceAssetHead = fn
	t.Cleanup(func() { lanceAssetHead = old })
}

func TestDeployWorkerInstances_BothUnits(t *testing.T) {
	m := &Manager{User: "root", Version: "4.44"}
	op := newFakeOperator()
	op.outputFn = func(cmd string) ([]byte, error) { return []byte("Linux x86_64\n"), nil }
	var probedURL string
	stubLanceAssetHead(t, func(url string) (int, error) { probedURL = url; return 200, nil })

	w := &spec.WorkerServerSpec{Ip: "10.0.0.10", Namespace: "http://10.0.0.51:9101", LanceMetricsPort: 9328}
	if err := m.deployWorkerInstances(op, []string{"10.0.0.100:23646"}, w, 0); err != nil {
		t.Fatalf("deployWorkerInstances: %v", err)
	}

	wantURL := "https://github.com/seaweedfs/seaweedfs/releases/download/4.44/weed-worker_linux_amd64.tar.gz"
	if probedURL != wantURL {
		t.Errorf("asset probe URL: got %q want %q", probedURL, wantURL)
	}

	goUnit, lanceUnit := workerInstallScripts(t, op)
	if !goUnit || !lanceUnit {
		t.Fatalf("expected both worker units (go=%v lance=%v); uploads: %v", goUnit, lanceUnit, keys(op.uploads))
	}

	for path, content := range op.uploads {
		if strings.HasSuffix(path, "/install_worker-lance0.sh") {
			for _, want := range []string{
				"BINARY=weed-worker",
				"weed-worker_${OS}_${SUFFIX}.tar.gz",
				"ExecStart=${BIN_DIR}/${BINARY} --admin 10.0.0.100:23646 --namespace http://10.0.0.51:9101 --metrics-port 9328 --metrics-ip 0.0.0.0",
			} {
				if !strings.Contains(content, want) {
					t.Errorf("lance install script missing %q", want)
				}
			}
		}
		if strings.HasSuffix(path, "/install_worker0.sh") {
			if !strings.Contains(content, "BINARY=weed") || strings.Contains(content, "BINARY=weed-worker") {
				t.Errorf("go worker install script should install weed")
			}
		}
	}
}

func TestDeployWorkerInstances_SkipsLanceOffPlatform(t *testing.T) {
	for _, uname := range []string{"Darwin arm64", "Linux armv7l", "Linux riscv64"} {
		m := &Manager{User: "root", Version: "4.44"}
		op := newFakeOperator()
		op.outputFn = func(cmd string) ([]byte, error) { return []byte(uname + "\n"), nil }
		stubLanceAssetHead(t, func(url string) (int, error) {
			t.Errorf("uname %q: no asset probe expected for an unsupported platform", uname)
			return 0, nil
		})

		w := &spec.WorkerServerSpec{Ip: "10.0.0.10", Namespace: "http://10.0.0.51:9101"}
		if err := m.deployWorkerInstances(op, []string{"10.0.0.100:23646"}, w, 0); err != nil {
			t.Fatalf("uname %q: deployWorkerInstances should not fail the host: %v", uname, err)
		}
		goUnit, lanceUnit := workerInstallScripts(t, op)
		if !goUnit {
			t.Errorf("uname %q: the Go worker unit must always deploy", uname)
		}
		if lanceUnit {
			t.Errorf("uname %q: the Lance unit should be skipped", uname)
		}
	}
}

func TestDeployWorkerInstances_SkipsLanceWithoutNamespace(t *testing.T) {
	m := &Manager{User: "root", Version: "4.44"}
	op := newFakeOperator()
	op.outputFn = func(cmd string) ([]byte, error) { return []byte("Linux x86_64\n"), nil }
	stubLanceAssetHead(t, func(url string) (int, error) {
		t.Error("no asset probe expected when the namespace is missing")
		return 0, nil
	})

	w := &spec.WorkerServerSpec{Ip: "10.0.0.10"} // Namespace empty after prepare()
	if err := m.deployWorkerInstances(op, []string{"10.0.0.100:23646"}, w, 0); err != nil {
		t.Fatalf("deployWorkerInstances should not fail the host: %v", err)
	}
	goUnit, lanceUnit := workerInstallScripts(t, op)
	if !goUnit {
		t.Error("the Go worker unit must always deploy")
	}
	if lanceUnit {
		t.Error("the Lance unit should be skipped without a namespace")
	}
	for _, cmd := range op.executed {
		if strings.Contains(cmd, "uname") {
			t.Errorf("no uname probe expected when the namespace is missing, got %q", cmd)
		}
	}
}

func TestDeployWorkerInstances_SkipsLanceOnOlderRelease(t *testing.T) {
	m := &Manager{User: "root", Version: "4.44"}
	op := newFakeOperator()
	op.outputFn = func(cmd string) ([]byte, error) { return []byte("Linux x86_64\n"), nil }
	stubLanceAssetHead(t, func(url string) (int, error) { return 404, nil })

	w := &spec.WorkerServerSpec{Ip: "10.0.0.10", Namespace: "http://10.0.0.51:9101"}
	if err := m.deployWorkerInstances(op, []string{"10.0.0.100:23646"}, w, 0); err != nil {
		t.Fatalf("a release without the asset should not fail the host: %v", err)
	}
	goUnit, lanceUnit := workerInstallScripts(t, op)
	if !goUnit {
		t.Error("the Go worker unit must always deploy")
	}
	if lanceUnit {
		t.Error("the Lance unit should be skipped when the release lacks the asset")
	}
}

// Only a definite 404 reads as "predates weed-worker"; anything else must
// error, not skip.
func TestDeployWorkerInstances_ProbeFailureIsAnError(t *testing.T) {
	for name, fn := range map[string]func(url string) (int, error){
		"http 403":      func(string) (int, error) { return 403, nil },
		"network error": func(string) (int, error) { return 0, errors.New("dial tcp: timeout") },
	} {
		m := &Manager{User: "root", Version: "4.44"}
		op := newFakeOperator()
		op.outputFn = func(cmd string) ([]byte, error) { return []byte("Linux x86_64\n"), nil }
		stubLanceAssetHead(t, fn)

		w := &spec.WorkerServerSpec{Ip: "10.0.0.10", Namespace: "http://10.0.0.51:9101"}
		err := m.deployWorkerInstances(op, []string{"10.0.0.100:23646"}, w, 0)
		if err == nil {
			t.Errorf("%s: expected an error, not a silent skip", name)
		}
		if goUnit, _ := workerInstallScripts(t, op); !goUnit {
			t.Errorf("%s: the Go worker unit deploys before the probe", name)
		}
	}
}

func TestLanceWorkerAssetArch(t *testing.T) {
	cases := map[string]string{ // uname output -> asset arch ("" = skip)
		"Linux x86_64":  "amd64",
		"Linux amd64":   "amd64",
		"Linux aarch64": "arm64",
		"Linux arm64":   "arm64",
		"Darwin arm64":  "",
		"Linux armv7l":  "",
		"Linux riscv64": "",
		"garbage":       "",
	}
	for uname, wantArch := range cases {
		arch, reason := lanceWorkerAssetArch(uname)
		if arch != wantArch {
			t.Errorf("uname %q: arch %q, want %q", uname, arch, wantArch)
		}
		if (wantArch == "") == (reason == "") {
			t.Errorf("uname %q: arch %q and reason %q should be mutually exclusive", uname, arch, reason)
		}
	}
}

func TestRustWorkerArgs(t *testing.T) {
	var buf bytes.Buffer
	w := &spec.WorkerServerSpec{Ip: "10.0.0.10", Namespace: "http://10.0.0.51:9101"}
	w.WriteLanceToBuffer([]string{"10.0.0.100:23646"}, &buf)

	got := rustWorkerArgs(&buf)
	want := "--admin 10.0.0.100:23646 --namespace http://10.0.0.51:9101"
	if got != want {
		t.Fatalf("rustWorkerArgs: got %q want %q", got, want)
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
