package manager

import (
	"fmt"
	"strings"

	"github.com/seaweedfs/seaweed-up/pkg/operator"
)

// shellSingleQuote wraps s in single quotes so it is safe to embed in a POSIX
// shell command. Any single quote inside s is escaped by closing the quoted
// string, inserting an escaped quote, and reopening: ' -> '\''.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

type Manager struct {
	User               string // username to login to the SSH server
	IdentityFile       string // path to the private key file
	UsePassword        bool   // use password instead of identity file for ssh connection
	ProxyUrl           string // proxy URL for binary download
	ComponentToDeploy  string
	Version            string
	SshPort            int
	PrepareVolumeDisks bool
	HostPrep           bool
	ForceRestart       bool
	// Concurrency limits the number of concurrent per-host deploy goroutines.
	// If <=0, deploys run with unlimited concurrency (default behavior).
	Concurrency int

	skipConfig bool
	skipEnable bool
	skipStart  bool
	sudoPass   string
	confDir    string
	dataDir    string

	// prepareHostAddressFn overrides PrepareHostAddress for tests. When nil,
	// PrepareAllHosts calls PrepareHostAddress directly.
	prepareHostAddressFn func(ip string, sshPort int) error
}

func NewManager() *Manager {
	return &Manager{
		skipConfig: false,
		skipEnable: false,
		skipStart:  false,
		Version:    "",
		sudoPass:   "",
	}
}

func info(message string) {
	fmt.Println("[INFO] " + message)
}

func (m *Manager) sudo(op operator.CommandOperator, cmd string) error {
	info("[execute] " + cmd)
	if m.sudoPass == "" {
		return op.Execute(cmd)
	}
	defer fmt.Println()
	return op.Execute(fmt.Sprintf("echo %s | sudo -S %s", shellSingleQuote(m.sudoPass), cmd))
}
