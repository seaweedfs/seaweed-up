package manager

import (
	"fmt"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
	"sync"
)

type MDynamicConfig struct {
	sync.RWMutex
	Changed              bool
	DynamicVolumeServers map[string][]spec.FolderSpec
}

type Manager struct {
	User               string // username to login to the SSH server
	IdentityFile       string // path to the private key file
	UsePassword        bool   // use password instead of identity file for ssh connection
	ProxyUrl           string // proxy URL for binary download
	ComponentToDeploy  string
	Version            string // seaweed version
	EnvoyVersion       string // envoy version
	SshPort            int
	PrepareVolumeDisks bool
	DynamicConfigFile  string         // filename of dynamic configuration file
	DynamicConfig      MDynamicConfig // dynamic configuration
	ForceRestart       bool

	skipConfig bool
	skipEnable bool
	skipStart  bool
	sudoPass   string
	confDir    string
	dataDir    string
}

func NewManager() *Manager {
	return &Manager{
		skipConfig: false,
		skipEnable: false,
		skipStart:  false,
		Version:    "",
		sudoPass:   "",
		DynamicConfig: MDynamicConfig{
			DynamicVolumeServers: make(map[string][]spec.FolderSpec),
		},
	}
}

func info(message string) {
	fmt.Println("[INFO] " + message)
}

func (m *Manager) sudo(op operator.CommandOperator, cmd string) error {
	info("[execute sudo] " + cmd)
	//if m.sudoPass == "" {
	//	return op.Execute(cmd)
	//}
	defer fmt.Println()
	return op.Execute(fmt.Sprintf("echo '%s' | sudo -S %s", m.sudoPass, cmd))
}
