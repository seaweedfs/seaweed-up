package cmd

import (
	"fmt"
	"runtime"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var (
	// Version information (will be set by build)
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
	GoVersion = runtime.Version()
)

func newVersionCmd() *cobra.Command {
	var (
		short bool
	)
	
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Show seaweed-up version information",
		Long: `Display version information for seaweed-up including version number,
git commit, build time, and Go version used for compilation.`,
		
		Example: `  # Show full version information
  seaweed-up version
  
  # Show only version number
  seaweed-up version --short`,
		
		RunE: func(cmd *cobra.Command, args []string) error {
			return runVersion(short)
		},
	}
	
	cmd.Flags().BoolVarP(&short, "short", "s", false, "show only version number")
	
	return cmd
}

func runVersion(short bool) error {
	if short {
		fmt.Println(Version)
		return nil
	}
	
	color.Green("🔧 seaweed-up - SeaweedFS Cluster Management Tool")
	fmt.Println()
	
	fmt.Printf("Version:    %s\n", Version)
	fmt.Printf("Commit:     %s\n", Commit)
	fmt.Printf("Build Time: %s\n", BuildTime)
	fmt.Printf("Go Version: %s\n", GoVersion)
	fmt.Printf("OS/Arch:    %s/%s\n", runtime.GOOS, runtime.GOARCH)
	
	return nil
}
