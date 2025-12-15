package utils

import (
	"bufio"
	"fmt"
	"os"
	"os/user"
	"strings"
	"syscall"

	"github.com/fatih/color"
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
func CurrentUser() (string, error) {
	currentUser, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("failed to get current user: %w", err)
	}
	return currentUser.Username, nil
}

// UserHome returns the current user's home directory
func UserHome() (string, error) {
	currentUser, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("failed to get current user home: %w", err)
	}
	return currentUser.HomeDir, nil
}

// PromptForInput reads a line of input from the user
func PromptForInput(message string) string {
	fmt.Print(message)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return strings.TrimSpace(scanner.Text())
	}
	return ""
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
