package utils

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"syscall"

	"github.com/fatih/color"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/term"
)

// PromptForConfirmation asks user for yes/no confirmation
func PromptForConfirmation(message string) bool {
	color.Yellow("❓ %s (y/N): ", message)

	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		response := strings.TrimSpace(strings.ToLower(scanner.Text()))
		return response == "y" || response == "yes"
	}

	return false
}

// CurrentUser returns the current username
func CurrentUser() string {
	currentUser, err := user.Current()
	if err != nil {
		log.Printf("Get current user: %s", err)
		return "root"
	}
	return currentUser.Username
}

// UserHome returns the current user's home directory
func UserHome() string {
	currentUser, err := user.Current()
	if err != nil {
		log.Printf("Get current user home: %s", err)
		return "/root"
	}
	return currentUser.HomeDir
}

// IsExist checks if a file or directory exists
func IsExist(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// EnsureDir creates directory if it doesn't exist
func EnsureDir(dir string) error {
	if !IsExist(dir) {
		return os.MkdirAll(dir, 0755)
	}
	return nil
}

// FormatBytes formats bytes into human readable format
func FormatBytes(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)

	switch {
	case bytes >= TB:
		return fmt.Sprintf("%.1fTB", float64(bytes)/TB)
	case bytes >= GB:
		return fmt.Sprintf("%.1fGB", float64(bytes)/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.1fMB", float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.1fKB", float64(bytes)/KB)
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}

// ParseComponentVersion parses component:version string
func ParseComponentVersion(spec string) (component, version string) {
	parts := strings.Split(spec, ":")
	component = parts[0]
	if len(parts) > 1 {
		version = parts[1]
	}
	return
}

// ReadPassword reads a password from stdin without echoing
func ReadPassword() string {
	bytePassword, err := term.ReadPassword(int(syscall.Stdin))
	if err != nil {
		log.Printf("Error reading password: %v", err)
		return ""
	}
	fmt.Println() // Add a newline after password input
	return string(bytePassword)
}

// ValidateClusterName checks if cluster name is valid
func ValidateClusterName(name string) error {
	if name == "" {
		return fmt.Errorf("cluster name cannot be empty")
	}

	// Check for invalid characters
	invalidChars := []string{" ", "\t", "\n", "/", "\\", ":", "*", "?", "\"", "<", ">", "|"}
	for _, char := range invalidChars {
		if strings.Contains(name, char) {
			return fmt.Errorf("cluster name contains invalid character: '%s'", char)
		}
	}

	return nil
}

// FormatDuration formats duration in human readable format
func FormatDuration(seconds int64) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	} else if seconds < 3600 {
		return fmt.Sprintf("%dm%ds", seconds/60, seconds%60)
	} else if seconds < 86400 {
		return fmt.Sprintf("%dh%dm", seconds/3600, (seconds%3600)/60)
	} else {
		return fmt.Sprintf("%dd%dh", seconds/86400, (seconds%86400)/3600)
	}
}

// Nvl returns the first non-empty string
func Nvl(values ...string) string {
	for _, s := range values {
		if s != "" {
			return s
		}
	}
	return ""
}

// NvlInt returns the first non-zero integer
func NvlInt(values ...int) int {
	for _, s := range values {
		if s != 0 {
			return s
		}
	}
	return 0
}

// PromptForPassword reads a password input from console
func PromptForPassword(format string, a ...interface{}) string {
	defer fmt.Println("")

	fmt.Printf(format, a...)

	input, err := term.ReadPassword(syscall.Stdin)
	if err != nil {
		return ""
	}
	return string(input)
}

// LoadPrivateKey loads a private key from file
func LoadPrivateKey(keyPath string) ([]byte, error) {
	key, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("unable to read private key: %w", err)
	}
	return key, nil
}

// SSHAgent returns SSH agent authentication method
func SSHAgent() (ssh.AuthMethod, error) {
	sshAgent, err := net.Dial("unix", os.Getenv("SSH_AUTH_SOCK"))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to SSH agent: %w", err)
	}

	return ssh.PublicKeysCallback(agent.NewClient(sshAgent).Signers), nil
}

// ExecuteCommand executes a local command and returns output
func ExecuteCommand(command string) (string, error) {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return "", fmt.Errorf("empty command")
	}

	cmd := exec.Command(parts[0], parts[1:]...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}
