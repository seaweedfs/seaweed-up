package manager

import (
	"bytes"
	"fmt"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
)

// workerComponentInstance returns the canonical worker instance name used for
// data directories and systemd unit naming (e.g. "worker0").
func workerComponentInstance(index int) string {
	return fmt.Sprintf("worker%d", index)
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
		component := "worker"
		componentInstance := workerComponentInstance(index)
		var buf bytes.Buffer
		w.WriteToBuffer(admins, &buf)

		return m.deployComponentInstance(op, component, componentInstance, &buf)
	})
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
