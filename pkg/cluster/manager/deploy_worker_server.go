package manager

import (
	"bytes"
	"fmt"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
)

func (m *Manager) DeployWorkerServer(admins []string, w *spec.WorkerServerSpec, index int) error {
	return operator.ExecuteRemote(fmt.Sprintf("%s:%d", w.Ip, w.PortSsh), m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {

		component := "worker"
		componentInstance := fmt.Sprintf("%s%d", component, index)
		var buf bytes.Buffer
		w.WriteToBuffer(admins, &buf)

		return m.deployComponentInstance(op, component, componentInstance, &buf)

	})
}

func (m *Manager) ResetWorkerServer(w *spec.WorkerServerSpec, index int) error {
	return operator.ExecuteRemote(fmt.Sprintf("%s:%d", w.Ip, w.PortSsh), m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {
		component := "worker"
		componentInstance := fmt.Sprintf("%s%d", component, index)
		return m.sudo(op, fmt.Sprintf("rm -Rf %s/%s/*", m.dataDir, componentInstance))
	})
}

func (m *Manager) StartWorkerServer(w *spec.WorkerServerSpec, index int) error {
	return operator.ExecuteRemote(fmt.Sprintf("%s:%d", w.Ip, w.PortSsh), m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {
		component := "worker"
		componentInstance := fmt.Sprintf("%s%d", component, index)
		return m.sudo(op, fmt.Sprintf("systemctl start seaweed_%s.service", componentInstance))
	})
}

func (m *Manager) StopWorkerServer(w *spec.WorkerServerSpec, index int) error {
	return operator.ExecuteRemote(fmt.Sprintf("%s:%d", w.Ip, w.PortSsh), m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {
		component := "worker"
		componentInstance := fmt.Sprintf("%s%d", component, index)
		return m.sudo(op, fmt.Sprintf("systemctl stop seaweed_%s.service", componentInstance))
	})
}
