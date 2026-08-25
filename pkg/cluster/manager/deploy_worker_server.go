package manager

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
)

// workerComponentInstance returns the canonical worker instance name used for
// data directories and systemd unit naming (e.g. "worker0").
func workerComponentInstance(index int) string {
	return fmt.Sprintf("worker%d", index)
}

// workerLanceComponent is the companion Rust weed-worker unit on every worker
// host; it maintains Lance tables, which the Go worker's job types cannot.
const workerLanceComponent = "worker-lance"

// workerLanceComponentInstance returns the companion unit's instance name.
func workerLanceComponentInstance(index int) string {
	return fmt.Sprintf("%s%d", workerLanceComponent, index)
}

// runWorkerRemote executes fn against the worker host over SSH.
func (m *Manager) runWorkerRemote(w *spec.WorkerServerSpec, fn func(op operator.CommandOperator) error) error {
	return operator.ExecuteRemote(fmt.Sprintf("%s:%d", w.Ip, w.PortSsh), m.User, m.IdentityFile, m.sudoPass, fn)
}

// runWorkerSystemctl runs a `systemctl <action> seaweed_<instance>.service`
// command on the remote worker host, eliminating boilerplate duplication
// between the Start/Stop/Reset helpers (mirrors the PR43 runS3Systemctl pattern).
func (m *Manager) runWorkerSystemctl(w *spec.WorkerServerSpec, index int, action string) error {
	return m.runWorkerRemote(w, func(op operator.CommandOperator) error {
		return m.sudo(op, fmt.Sprintf("systemctl %s seaweed_%s.service", action, workerComponentInstance(index)))
	})
}

func (m *Manager) DeployWorkerServer(admins []string, w *spec.WorkerServerSpec, index int) error {
	return m.runWorkerRemote(w, func(op operator.CommandOperator) error {
		return m.deployWorkerInstances(op, admins, w, index)
	})
}

// deployWorkerInstances installs the Go unit and, when the host qualifies,
// the companion Lance unit in one SSH session.
func (m *Manager) deployWorkerInstances(op operator.CommandOperator, admins []string, w *spec.WorkerServerSpec, index int) error {
	var buf bytes.Buffer
	w.WriteToBuffer(admins, &buf)
	if err := m.deployComponentInstance(op, "worker", workerComponentInstance(index), &buf); err != nil {
		return err
	}
	return m.deployLanceWorker(op, admins, w, index)
}

// deployLanceWorker installs the companion Rust weed-worker unit; it skips,
// never fails the host, when the host or release cannot run it.
func (m *Manager) deployLanceWorker(op operator.CommandOperator, admins []string, w *spec.WorkerServerSpec, index int) error {
	if w.Namespace == "" {
		m.info(fmt.Sprintf("worker %s: no Lance namespace resolvable (no s3_servers and no worker_servers[].namespace); skipping the Lance maintenance worker", w.Ip))
		return nil
	}
	uname, err := op.Output("uname -s -m")
	if err != nil {
		m.info(fmt.Sprintf("worker %s: cannot detect the host platform (%v); skipping the Lance maintenance worker", w.Ip, err))
		return nil
	}
	arch, reason := lanceWorkerAssetArch(string(uname))
	if reason != "" {
		m.info(fmt.Sprintf("worker %s: %s; skipping the Lance maintenance worker", w.Ip, reason))
		return nil
	}

	// Probed from the control host so a 404 skips instead of failing
	// mid-install; only a definite 404 means "predates weed-worker".
	owner, repo := m.ReleaseOwnerRepo()
	asset := fmt.Sprintf("weed-worker_linux_%s.tar.gz", arch)
	assetURL := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s", owner, repo, m.Version, asset)
	switch status, err := lanceAssetHead(assetURL); {
	case err != nil:
		return fmt.Errorf("check the weed-worker release asset %s: %w", assetURL, err)
	case status == http.StatusNotFound:
		m.info(fmt.Sprintf("worker %s: release %s does not ship %s (it predates the Rust worker); skipping the Lance maintenance worker", w.Ip, m.Version, asset))
		return nil
	case status != http.StatusOK:
		return fmt.Errorf("check the weed-worker release asset %s: HTTP %d", assetURL, status)
	}

	var buf bytes.Buffer
	w.WriteLanceToBuffer(admins, &buf)
	return m.deployComponentInstance(op, workerLanceComponent, workerLanceComponentInstance(index), &buf)
}

// lanceAssetHead issues the probe's HEAD; a var so tests stub the network away.
var lanceAssetHead = func(url string) (int, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Head(url)
	if err != nil {
		return 0, err
	}
	// A HEAD carries no body worth reading; the status is the answer.
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

// lanceWorkerAssetArch maps `uname -s -m` to the release asset arch, or gives
// the reason weed-worker cannot run there (linux amd64/arm64 only).
func lanceWorkerAssetArch(unameOut string) (arch, reason string) {
	fields := strings.Fields(unameOut)
	if len(fields) < 2 {
		return "", fmt.Sprintf("unexpected uname output %q", strings.TrimSpace(unameOut))
	}
	osName, machine := fields[0], fields[1]
	if !strings.EqualFold(osName, "Linux") {
		return "", fmt.Sprintf("weed-worker is published for linux only; host reports %s", osName)
	}
	switch machine {
	case "x86_64", "amd64":
		return "amd64", ""
	case "aarch64", "arm64":
		return "arm64", ""
	}
	return "", fmt.Sprintf("weed-worker is published for linux amd64/arm64 only; host is %s", machine)
}

func (m *Manager) ResetWorkerServer(w *spec.WorkerServerSpec, index int) error {
	return m.runWorkerRemote(w, func(op operator.CommandOperator) error {
		return m.sudo(op, fmt.Sprintf("rm -Rf %s/%s/*", m.dataDir, workerComponentInstance(index)))
	})
}

func (m *Manager) StartWorkerServer(w *spec.WorkerServerSpec, index int) error {
	return m.runWorkerSystemctl(w, index, "start")
}

func (m *Manager) StopWorkerServer(w *spec.WorkerServerSpec, index int) error {
	return m.runWorkerSystemctl(w, index, "stop")
}
