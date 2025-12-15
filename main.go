package main

import (
	"fmt"
	"os"

	"github.com/fatih/color"
	"github.com/seaweedfs/seaweed-up/cmd"
	"github.com/seaweedfs/seaweed-up/pkg/errors"
)

func main() {
	if err := cmd.Execute(); err != nil {
		handleError(err)
		os.Exit(1)
	}
}

func handleError(err error) {
	// Enhanced error handling with structured error types
	switch e := err.(type) {
	case *errors.SSHConnectionError:
		handleSSHError(e)
	case *errors.ClusterOperationError:
		handleClusterError(e)
	case *errors.ComponentError:
		handleComponentError(e)
	default:
		// Generic error handling
		color.Red("❌ Error: %v", err)
	}
}

func handleSSHError(err *errors.SSHConnectionError) {
	color.Red("🔐 SSH Connection Failed")
	fmt.Printf("Host: %s:%d\n", err.Host, err.Port)
	fmt.Printf("User: %s\n", err.User)
	fmt.Printf("Error: %s\n", err.Message)
	fmt.Println()
	
	color.Yellow("💡 Suggestions:")
	fmt.Println("- Verify the host is reachable: ping", err.Host)
	fmt.Println("- Check SSH service is running on target host")
	fmt.Println("- Verify SSH key permissions (should be 600)")
	fmt.Println("- Test connection manually: ssh", fmt.Sprintf("%s@%s", err.User, err.Host))
	fmt.Println("- Check SSH agent: ssh-add -l")
}

func handleClusterError(err *errors.ClusterOperationError) {
	color.Red("🏗️  Cluster Operation Failed")
	fmt.Printf("Operation: %s\n", err.Operation)
	fmt.Printf("Cluster: %s\n", err.ClusterName)
	if err.Node != "" {
		fmt.Printf("Node: %s\n", err.Node)
	}
	fmt.Printf("Error: %s\n", err.Message)
	fmt.Println()
	
	color.Yellow("💡 Suggestions:")
	fmt.Println("- Check cluster status: seaweed-up cluster status", err.ClusterName)
	fmt.Println("- Review cluster configuration file")
	fmt.Println("- Verify all nodes are accessible")
}

func handleComponentError(err *errors.ComponentError) {
	color.Red("📦 Component Error")
	fmt.Printf("Component: %s\n", err.Component)
	if err.Version != "" {
		fmt.Printf("Version: %s\n", err.Version)
	}
	fmt.Printf("Action: %s\n", err.Action)
	fmt.Printf("Error: %s\n", err.Message)
	fmt.Println()
	
	color.Yellow("💡 Suggestions:")
	fmt.Println("- List available components: seaweed-up component list")
	fmt.Println("- Check component status: seaweed-up component list --installed")
	fmt.Println("- Try with a different version")
}
