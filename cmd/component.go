package cmd

import (
	"context"
	"fmt"

	"github.com/fatih/color"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
	
	"github.com/seaweedfs/seaweed-up/pkg/component/registry"
	"github.com/seaweedfs/seaweed-up/pkg/component/repository"
	"github.com/seaweedfs/seaweed-up/pkg/utils"
)

func newComponentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "component",
		Short: "Manage SeaweedFS components",
		Long: `Component management commands for SeaweedFS binaries.

This command group provides component lifecycle management including:
- Install specific versions of SeaweedFS components
- List available and installed versions
- Update components to latest versions
- Uninstall unused components`,
		
		Example: `  # Install specific version
  seaweed-up component install master:3.75
  
  # List available versions
  seaweed-up component list master
  
  # Update all components to latest
  seaweed-up component update --all`,
	}
	
	// Add component subcommands
	cmd.AddCommand(newComponentInstallCmd())
	cmd.AddCommand(newComponentListCmd())
	cmd.AddCommand(newComponentUpdateCmd())
	cmd.AddCommand(newComponentUninstallCmd())
	
	return cmd
}

func newComponentInstallCmd() *cobra.Command {
	var (
		version string
		force   bool
	)
	
	cmd := &cobra.Command{
		Use:   "install <component>[:<version>]",
		Short: "Install SeaweedFS components",
		Long: `Install specific versions of SeaweedFS components.

Components are downloaded from official releases and cached locally
for fast deployment to clusters.

Supported components: master, volume, filer, s3, webdav, mount`,
		
		Example: `  # Install latest version of master
  seaweed-up component install master
  
  # Install specific version
  seaweed-up component install master:3.75
  
  # Install multiple components
  seaweed-up component install master:3.75 volume:3.75 filer:3.75`,
		
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComponentInstall(args, &ComponentInstallOptions{
				Version: version,
				Force:   force,
			})
		},
	}
	
	cmd.Flags().StringVar(&version, "version", "", "specific version to install")
	cmd.Flags().BoolVar(&force, "force", false, "force reinstall if already installed")
	
	return cmd
}

func newComponentListCmd() *cobra.Command {
	var (
		installedOnly bool
		available     bool
		jsonOutput    bool
		verbose       bool
	)
	
	cmd := &cobra.Command{
		Use:   "list [component]",
		Short: "List SeaweedFS components and versions",
		Long: `List available and installed SeaweedFS components.

Shows component versions, installation status, and metadata.
Without arguments, lists all components. With component name,
shows available versions for that component.`,
		
		Example: `  # List all installed components
  seaweed-up component list --installed
  
  # List available versions for master
  seaweed-up component list master
  
  # List all available components
  seaweed-up component list --available`,
		
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComponentList(args, &ComponentListOptions{
				InstalledOnly: installedOnly,
				Available:     available,
				JSONOutput:    jsonOutput,
				Verbose:       verbose,
			})
		},
	}
	
	cmd.Flags().BoolVar(&installedOnly, "installed", false, "show only installed components")
	cmd.Flags().BoolVar(&available, "available", false, "show all available versions")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output in JSON format")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "show detailed information")
	
	return cmd
}

func newComponentUpdateCmd() *cobra.Command {
	var (
		all       bool
		version   string
		skipConfirm bool
	)
	
	cmd := &cobra.Command{
		Use:   "update [component...]",
		Short: "Update SeaweedFS components",
		Long: `Update installed components to newer versions.

Can update specific components or all installed components
to the latest available versions.`,
		
		Example: `  # Update all components to latest
  seaweed-up component update --all
  
  # Update specific component
  seaweed-up component update master
  
  # Update to specific version
  seaweed-up component update master --version=3.75`,
		
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComponentUpdate(args, &ComponentUpdateOptions{
				All:         all,
				Version:     version,
				SkipConfirm: skipConfirm,
			})
		},
	}
	
	cmd.Flags().BoolVar(&all, "all", false, "update all installed components")
	cmd.Flags().StringVar(&version, "version", "", "specific version to update to")
	cmd.Flags().BoolVarP(&skipConfirm, "yes", "y", false, "skip confirmation prompts")
	
	return cmd
}

func newComponentUninstallCmd() *cobra.Command {
	var (
		all         bool
		skipConfirm bool
	)
	
	cmd := &cobra.Command{
		Use:   "uninstall <component>[:<version>]",
		Short: "Uninstall SeaweedFS components",
		Long: `Uninstall specific versions of SeaweedFS components.

This removes components from the local cache. Components
currently used by running clusters will be protected.`,
		
		Example: `  # Uninstall specific version
  seaweed-up component uninstall master:3.74
  
  # Uninstall all versions of a component
  seaweed-up component uninstall master --all`,
		
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComponentUninstall(args, &ComponentUninstallOptions{
				All:         all,
				SkipConfirm: skipConfirm,
			})
		},
	}
	
	cmd.Flags().BoolVar(&all, "all", false, "uninstall all versions of the component")
	cmd.Flags().BoolVarP(&skipConfirm, "yes", "y", false, "skip confirmation prompts")
	
	return cmd
}

// Component command option structs
type ComponentInstallOptions struct {
	Version string
	Force   bool
}

type ComponentListOptions struct {
	InstalledOnly bool
	Available     bool
	JSONOutput    bool
	Verbose       bool
}

type ComponentUpdateOptions struct {
	All         bool
	Version     string
	SkipConfirm bool
}

type ComponentUninstallOptions struct {
	All         bool
	SkipConfirm bool
}

// Component command implementations
func runComponentInstall(args []string, opts *ComponentInstallOptions) error {
	color.Green("📦 Installing SeaweedFS components...")
	
	// Initialize component registry
	registry, err := getComponentRegistry()
	if err != nil {
		return err
	}
	
	// Initialize repository
	repo := getRepository(registry, "")
	
	for _, componentSpec := range args {
		component, version := utils.ParseComponentVersion(componentSpec)
		
		// For SeaweedFS, all components are part of the same binary
		if component != "master" && component != "volume" && component != "filer" && 
		   component != "s3" && component != "webdav" && component != "mount" &&
		   component != "seaweedfs" {
			color.Yellow("⚠️  Warning: '%s' is not a valid SeaweedFS component", component)
			color.Cyan("💡 Valid components: master, volume, filer, s3, webdav, mount, seaweedfs")
			continue
		}
		
		// Use "seaweedfs" as the actual component name (single binary)
		actualComponent := "seaweedfs"
		
		// If version not specified, get latest
		if version == "" {
			if opts.Version != "" {
				version = opts.Version
			} else {
				fmt.Printf("🔍 Finding latest version...\n")
				latest, err := repo.GetLatestVersion(context.Background())
				if err != nil {
					return fmt.Errorf("failed to get latest version: %w", err)
				}
				version = latest
			}
		}
		
		// Check if already installed and not forcing
		if registry.IsInstalled(actualComponent, version) && !opts.Force {
			color.Yellow("⚠️  Component %s:%s is already installed", component, version)
			color.Cyan("💡 Use --force to reinstall")
			continue
		}
		
		fmt.Printf("📥 Downloading %s version %s...\n", component, version)
		
		// Download component
		installedComp, err := repo.DownloadComponent(context.Background(), version, true)
		if err != nil {
			return fmt.Errorf("failed to download %s:%s: %w", component, version, err)
		}
		
		color.Green("✅ Successfully installed %s:%s", component, version)
		fmt.Printf("   Binary: %s\n", installedComp.Path)
		fmt.Printf("   Size: %s\n", utils.FormatBytes(installedComp.Size))
	}
	
	color.Green("🎉 All components installed successfully!")
	return nil
}

func runComponentList(args []string, opts *ComponentListOptions) error {
	color.Green("📋 SeaweedFS Components")
	
	// Initialize component registry
	registry, err := getComponentRegistry()
	if err != nil {
		return err
	}
	
	if len(args) == 0 {
		// List all components
		if opts.InstalledOnly {
			return showInstalledComponents(registry, opts)
		} else if opts.Available {
			return showAvailableComponents(opts)
		} else {
			// Show both installed and available info
			return showAllComponents(registry, opts)
		}
	} else {
		// List specific component versions
		component := args[0]
		return showComponentVersions(registry, component, opts)
	}
}

func runComponentUpdate(args []string, opts *ComponentUpdateOptions) error {
	// Initialize component registry
	registry, err := getComponentRegistry()
	if err != nil {
		return err
	}
	
	// Initialize repository
	repo := getRepository(registry, "")
	
	if opts.All {
		color.Green("⬆️  Updating all components...")
		
		// Get all installed components
		installed := registry.ListInstalled()
		if len(installed) == 0 {
			color.Yellow("⚠️  No components are installed")
			return nil
		}
		
		// Update each component to latest version
		for componentName := range installed {
			if err := updateComponent(registry, repo, componentName, opts.Version, opts.SkipConfirm); err != nil {
				color.Red("❌ Failed to update %s: %v", componentName, err)
			}
		}
	} else {
		color.Green("⬆️  Updating components: %v", args)
		
		for _, componentSpec := range args {
			component, _ := utils.ParseComponentVersion(componentSpec)
			// Map component name to seaweedfs
			actualComponent := "seaweedfs"
			
			if err := updateComponent(registry, repo, actualComponent, opts.Version, opts.SkipConfirm); err != nil {
				return fmt.Errorf("failed to update %s: %w", component, err)
			}
		}
	}
	
	color.Green("✅ Components updated successfully!")
	return nil
}

func runComponentUninstall(args []string, opts *ComponentUninstallOptions) error {
	color.Yellow("🗑️  Uninstalling components: %v", args)
	
	// Initialize component registry
	registry, err := getComponentRegistry()
	if err != nil {
		return err
	}
	
	if !opts.SkipConfirm {
		if !utils.PromptForConfirmation("Are you sure you want to uninstall these components?") {
			color.Yellow("⚠️  Uninstall cancelled")
			return nil
		}
	}
	
	for _, componentSpec := range args {
		component, version := utils.ParseComponentVersion(componentSpec)
		actualComponent := "seaweedfs"
		
		if opts.All {
			// Uninstall all versions of the component
			versions := registry.ListVersions(actualComponent)
			if len(versions) == 0 {
				color.Yellow("⚠️  Component %s is not installed", component)
				continue
			}
			
			for _, v := range versions {
				if err := registry.Uninstall(actualComponent, v); err != nil {
					color.Red("❌ Failed to uninstall %s:%s: %v", component, v, err)
				} else {
					color.Green("✅ Uninstalled %s:%s", component, v)
				}
			}
		} else {
			if version == "" {
				// Get latest installed version
				latest, err := registry.GetLatestInstalled(actualComponent)
				if err != nil {
					return fmt.Errorf("failed to get latest installed version of %s: %w", component, err)
				}
				version = latest.Version
			}
			
			if err := registry.Uninstall(actualComponent, version); err != nil {
				return fmt.Errorf("failed to uninstall %s:%s: %w", component, version, err)
			}
			
			color.Green("✅ Uninstalled %s:%s", component, version)
		}
	}
	
	return nil
}

// Helper functions

func getComponentRegistry() (*registry.ComponentRegistry, error) {
	return registry.NewComponentRegistry()
}

func getRepository(registry *registry.ComponentRegistry, proxyURL string) *repository.GitHubRepository {
	return repository.NewGitHubRepository(registry, proxyURL)
}

func showInstalledComponents(registry *registry.ComponentRegistry, opts *ComponentListOptions) error {
	fmt.Println("📦 Installed Components:")
	
	installed := registry.ListInstalled()
	if len(installed) == 0 {
		fmt.Println("   No components installed")
		return nil
	}
	
	if opts.JSONOutput {
		return outputJSON(installed)
	}
	
	// Create table
	t := table.NewWriter()
	t.SetStyle(table.StyleLight)
	t.AppendHeader(table.Row{"Component", "Version", "Size", "Install Date", "Path"})
	
	for name, versions := range installed {
		for _, comp := range versions {
			t.AppendRow(table.Row{
				name,
				comp.Version,
				utils.FormatBytes(comp.Size),
				comp.InstallAt.Format("2006-01-02 15:04:05"),
				comp.Path,
			})
		}
	}
	
	fmt.Println(t.Render())
	
	// Show stats
	stats := registry.Stats()
	fmt.Printf("\n📊 Registry Stats:\n")
	fmt.Printf("   Components: %v\n", stats["total_components"])
	fmt.Printf("   Versions: %v\n", stats["total_versions"])
	fmt.Printf("   Total Size: %v\n", stats["total_size"])
	
	return nil
}

func showAvailableComponents(opts *ComponentListOptions) error {
	fmt.Println("🌐 Available Components from GitHub:")
	
	repo := repository.NewGitHubRepository(nil, "")
	
	versions, err := repo.ListVersions(context.Background())
	if err != nil {
		return fmt.Errorf("failed to fetch available versions: %w", err)
	}
	
	if opts.JSONOutput {
		return outputJSON(map[string]interface{}{
			"seaweedfs": map[string]interface{}{
				"versions": versions[:min(len(versions), 20)], // Show latest 20
			},
		})
	}
	
	fmt.Printf("📦 SeaweedFS (latest %d versions):\n", min(len(versions), 20))
	for i, version := range versions {
		if i >= 20 { // Limit display
			break
		}
		fmt.Printf("   %s\n", version)
	}
	
	if len(versions) > 20 {
		fmt.Printf("   ... and %d more versions\n", len(versions)-20)
	}
	
	return nil
}

func showAllComponents(registry *registry.ComponentRegistry, opts *ComponentListOptions) error {
	if err := showInstalledComponents(registry, opts); err != nil {
		return err
	}
	
	fmt.Println()
	return showAvailableComponents(opts)
}

func showComponentVersions(registry *registry.ComponentRegistry, component string, opts *ComponentListOptions) error {
	actualComponent := "seaweedfs"
	
	fmt.Printf("📦 Component: %s\n", component)
	
	// Show installed versions
	installedVersions := registry.ListVersions(actualComponent)
	if len(installedVersions) > 0 {
		fmt.Println("\n✅ Installed Versions:")
		for _, version := range installedVersions {
			comp, _ := registry.GetInstalled(actualComponent, version)
			fmt.Printf("   %s (%s, %s)\n", version, 
				utils.FormatBytes(comp.Size), 
				comp.InstallAt.Format("2006-01-02"))
		}
	} else {
		fmt.Println("\n❌ No versions installed")
	}
	
	// Show available versions if requested
	if opts.Available || !opts.InstalledOnly {
		repo := getRepository(registry, "")
		
		fmt.Println("\n🌐 Available Versions (latest 10):")
		versions, err := repo.ListVersions(context.Background())
		if err != nil {
			return fmt.Errorf("failed to fetch available versions: %w", err)
		}
		
		for i, version := range versions {
			if i >= 10 {
				break
			}
			
			status := ""
			if registry.IsInstalled(actualComponent, version) {
				status = " ✅"
			}
			fmt.Printf("   %s%s\n", version, status)
		}
	}
	
	return nil
}

func updateComponent(registry *registry.ComponentRegistry, repo *repository.GitHubRepository, component string, targetVersion string, skipConfirm bool) error {
	// Get current version
	current, err := registry.GetLatestInstalled(component)
	if err != nil {
		return fmt.Errorf("component %s not installed", component)
	}
	
	// Get target version
	if targetVersion == "" || targetVersion == "latest" {
		targetVersion, err = repo.GetLatestVersion(context.Background())
		if err != nil {
			return fmt.Errorf("failed to get latest version: %w", err)
		}
	}
	
	// Check if update needed
	if current.Version == targetVersion {
		color.Yellow("⚠️  %s:%s is already the latest version", component, current.Version)
		return nil
	}
	
	// Confirm update
	if !skipConfirm {
		if !utils.PromptForConfirmation(fmt.Sprintf("Update %s from %s to %s?", component, current.Version, targetVersion)) {
			color.Yellow("⚠️  Update cancelled")
			return nil
		}
	}
	
	// Download new version
	color.Cyan("📥 Downloading %s:%s...", component, targetVersion)
	_, err = repo.DownloadComponent(context.Background(), targetVersion, true)
	if err != nil {
		return fmt.Errorf("failed to download %s:%s: %w", component, targetVersion, err)
	}
	
	color.Green("✅ Updated %s from %s to %s", component, current.Version, targetVersion)
	return nil
}

func outputJSON(data interface{}) error {
	// Simple JSON output - could be enhanced with proper JSON marshaling
	fmt.Printf("%+v\n", data)
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
