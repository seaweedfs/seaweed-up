package cmd

import (
	"fmt"
	"os"

	"github.com/mitchellh/go-homedir"
	"github.com/seaweedfs/seaweed-up/pkg/environment"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile string
	verbose bool
	rootCmd = &cobra.Command{
		Use:   "seaweed-up",
		Short: "SeaweedFS cluster management tool",
		Long: `seaweed-up is a comprehensive tool for deploying, managing, 
and operating SeaweedFS clusters across multiple environments.

It provides enterprise-grade cluster lifecycle management including:
- Deployment and configuration management
- Real-time cluster monitoring and health checks  
- Rolling upgrades and scaling operations
- Component version management
- Backup and recovery operations`,
		
		Example: `  # Deploy a cluster from configuration
  seaweed-up cluster deploy -f cluster.yaml

  # Check cluster status
  seaweed-up cluster status my-cluster

  # Scale cluster by adding volume servers
  seaweed-up cluster scale out my-cluster --add-volume=2

  # Upgrade cluster to latest version
  seaweed-up cluster upgrade my-cluster --version=latest`,

		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Skip environment initialization for certain commands
			switch cmd.Name() {
			case "version", "help", "completion":
				return nil
			}
			return environment.InitGlobalEnv()
		},
		
		SilenceErrors: true,
		SilenceUsage:  true,
	}
)

// Execute executes the root command
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	cobra.OnInitialize(initConfig)
	
	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.seaweed-up.yaml)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")
	
	// Bind flags to viper
	viper.BindPFlag("verbose", rootCmd.PersistentFlags().Lookup("verbose"))
	
	// Add command groups
	rootCmd.AddCommand(newClusterCmd())
	rootCmd.AddCommand(newComponentCmd())
	rootCmd.AddCommand(newEnvCmd())
	rootCmd.AddCommand(newTemplateCmd())
	rootCmd.AddCommand(newVersionCmd())
}

// initConfig reads in config file and ENV variables
func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, err := homedir.Dir()
		cobra.CheckErr(err)
		
		viper.AddConfigPath(home)
		viper.SetConfigType("yaml")
		viper.SetConfigName(".seaweed-up")
	}
	
	viper.AutomaticEnv()
	
	if err := viper.ReadInConfig(); err != nil {
		// Config file not found is acceptable - we'll use defaults
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			// Config file was found but another error was produced (e.g., malformed YAML)
			fmt.Fprintf(os.Stderr, "Error reading config file: %v\n", err)
			os.Exit(1)
		}
	} else {
		if verbose {
			fmt.Fprintln(os.Stderr, "Using config file:", viper.ConfigFileUsed())
		}
	}
}
