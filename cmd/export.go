package cmd

import (
	"fmt"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/exporters"
	"github.com/seaweedfs/seaweed-up/pkg/plugins"
	"github.com/seaweedfs/seaweed-up/pkg/utils"
)

func newExportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export cluster configurations to different formats",
		Long: `Export SeaweedFS cluster configurations to various deployment formats.

Supports exporting to Kubernetes manifests, Docker Compose, Terraform,
and other infrastructure-as-code formats for easy integration with
existing deployment pipelines.`,
		Example: `  # Export to Kubernetes manifests
  seaweed-up export kubernetes -f cluster.yaml -o k8s-manifests.yaml

  # Export to Docker Compose
  seaweed-up export docker-compose -f cluster.yaml -o docker-compose.yml

  # Export with custom namespace
  seaweed-up export kubernetes -f cluster.yaml --namespace=production`,
	}

	cmd.AddCommand(newExportKubernetesCmd())
	cmd.AddCommand(newExportDockerComposeCmd())
	cmd.AddCommand(newExportTerraformCmd())
	cmd.AddCommand(newExportListFormatsCmd())

	return cmd
}

func newExportKubernetesCmd() *cobra.Command {
	var (
		configFile      string
		outputFile      string
		namespace       string
		imageRepository string
		imageTag        string
		serviceType     string
		storageClass    string
	)

	cmd := &cobra.Command{
		Use:   "kubernetes",
		Short: "Export to Kubernetes manifests",
		Long: `Export cluster configuration as Kubernetes manifests.

Generates deployments, services, configmaps, and other Kubernetes
resources for deploying SeaweedFS on Kubernetes clusters.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExportKubernetes(configFile, outputFile, namespace, imageRepository, imageTag, serviceType, storageClass)
		},
	}

	cmd.Flags().StringVarP(&configFile, "file", "f", "", "cluster configuration file (required)")
	cmd.Flags().StringVarP(&outputFile, "output", "o", "", "output file for Kubernetes manifests")
	cmd.Flags().StringVar(&namespace, "namespace", "seaweedfs", "Kubernetes namespace")
	cmd.Flags().StringVar(&imageRepository, "image-repo", "chrislusf/seaweedfs", "Docker image repository")
	cmd.Flags().StringVar(&imageTag, "image-tag", "latest", "Docker image tag")
	cmd.Flags().StringVar(&serviceType, "service-type", "ClusterIP", "Kubernetes service type")
	cmd.Flags().StringVar(&storageClass, "storage-class", "", "storage class for persistent volumes")

	cmd.MarkFlagRequired("file")

	return cmd
}

func newExportDockerComposeCmd() *cobra.Command {
	var (
		configFile      string
		outputFile      string
		imageRepository string
		imageTag        string
		networkName     string
	)

	cmd := &cobra.Command{
		Use:   "docker-compose",
		Short: "Export to Docker Compose format",
		Long: `Export cluster configuration as Docker Compose file.

Generates a docker-compose.yml file for deploying SeaweedFS
using Docker Compose with proper networking and volume mounts.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExportDockerCompose(configFile, outputFile, imageRepository, imageTag, networkName)
		},
	}

	cmd.Flags().StringVarP(&configFile, "file", "f", "", "cluster configuration file (required)")
	cmd.Flags().StringVarP(&outputFile, "output", "o", "", "output file for Docker Compose")
	cmd.Flags().StringVar(&imageRepository, "image-repo", "chrislusf/seaweedfs", "Docker image repository")
	cmd.Flags().StringVar(&imageTag, "image-tag", "latest", "Docker image tag")
	cmd.Flags().StringVar(&networkName, "network", "seaweedfs", "Docker network name")

	cmd.MarkFlagRequired("file")

	return cmd
}

func newExportTerraformCmd() *cobra.Command {
	var (
		configFile   string
		outputFile   string
		provider     string
		region       string
		instanceType string
	)

	cmd := &cobra.Command{
		Use:   "terraform",
		Short: "Export to Terraform configuration",
		Long: `Export cluster configuration as Terraform infrastructure code.

Generates Terraform configuration files for deploying SeaweedFS
infrastructure on cloud providers.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExportTerraform(configFile, outputFile, provider, region, instanceType)
		},
	}

	cmd.Flags().StringVarP(&configFile, "file", "f", "", "cluster configuration file (required)")
	cmd.Flags().StringVarP(&outputFile, "output", "o", "", "output file for Terraform configuration")
	cmd.Flags().StringVar(&provider, "provider", "aws", "cloud provider (aws|gcp|azure)")
	cmd.Flags().StringVar(&region, "region", "us-east-1", "cloud region")
	cmd.Flags().StringVar(&instanceType, "instance-type", "t3.medium", "instance type for servers")

	cmd.MarkFlagRequired("file")

	return cmd
}

func newExportListFormatsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list-formats",
		Short: "List available export formats",
		Long: `List all supported export formats and their capabilities.

Shows available formats, their descriptions, and example usage commands.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExportListFormats()
		},
	}

	return cmd
}

// Implementation functions

func runExportKubernetes(configFile, outputFile, namespace, imageRepository, imageTag, serviceType, storageClass string) error {
	color.Green("📦 Exporting to Kubernetes manifests")

	// Load cluster specification
	clusterSpec, err := loadClusterSpec(configFile)
	if err != nil {
		return fmt.Errorf("failed to load cluster configuration: %w", err)
	}

	color.Cyan("📋 Export Configuration:")
	fmt.Printf("Cluster: %s\n", clusterSpec.Name)
	fmt.Printf("Namespace: %s\n", namespace)
	fmt.Printf("Image: %s:%s\n", imageRepository, imageTag)
	fmt.Printf("Service Type: %s\n", serviceType)
	if storageClass != "" {
		fmt.Printf("Storage Class: %s\n", storageClass)
	}

	// Create Kubernetes exporter
	options := exporters.KubernetesExportOptions{
		Namespace:       namespace,
		ImageRepository: imageRepository,
		ImageTag:        imageTag,
		ServiceType:     serviceType,
		StorageClass:    storageClass,
	}

	exporter := exporters.NewKubernetesExporter(namespace, options)

	// Export to Kubernetes format
	manifests, err := exporter.Export(nil, clusterSpec, plugins.ExportFormatKubernetes)
	if err != nil {
		return fmt.Errorf("export failed: %w", err)
	}

	// Save to file or output to console
	if outputFile == "" {
		outputFile = fmt.Sprintf("%s-k8s-manifests.yaml", clusterSpec.Name)
	}

	if err := utils.WriteFile(outputFile, string(manifests)); err != nil {
		return fmt.Errorf("failed to write manifests: %w", err)
	}

	color.Green("✅ Kubernetes manifests exported successfully!")
	color.Cyan("📄 Generated resources:")
	fmt.Println("  • Namespace: " + namespace)
	fmt.Println("  • ConfigMap: seaweedfs-config")

	for i := range clusterSpec.MasterServers {
		fmt.Printf("  • Deployment: seaweedfs-master-%d\n", i)
		fmt.Printf("  • Service: seaweedfs-master-%d\n", i)
	}

	for i := range clusterSpec.VolumeServers {
		fmt.Printf("  • StatefulSet: seaweedfs-volume-%d\n", i)
		fmt.Printf("  • Service: seaweedfs-volume-%d\n", i)
	}

	for i := range clusterSpec.FilerServers {
		fmt.Printf("  • Deployment: seaweedfs-filer-%d\n", i)
		fmt.Printf("  • Service: seaweedfs-filer-%d\n", i)
	}

	color.Cyan("💾 Output saved to: %s", outputFile)
	color.Cyan("💡 Next steps:")
	fmt.Printf("  kubectl apply -f %s\n", outputFile)
	fmt.Printf("  kubectl get pods -n %s\n", namespace)

	return nil
}

func runExportDockerCompose(configFile, outputFile, imageRepository, imageTag, networkName string) error {
	color.Green("🐳 Exporting to Docker Compose format")

	// Load cluster specification
	clusterSpec, err := loadClusterSpec(configFile)
	if err != nil {
		return fmt.Errorf("failed to load cluster configuration: %w", err)
	}

	color.Cyan("📋 Export Configuration:")
	fmt.Printf("Cluster: %s\n", clusterSpec.Name)
	fmt.Printf("Image: %s:%s\n", imageRepository, imageTag)
	fmt.Printf("Network: %s\n", networkName)

	// Generate Docker Compose content
	compose := generateDockerCompose(clusterSpec, imageRepository, imageTag, networkName)

	// Save to file
	if outputFile == "" {
		outputFile = fmt.Sprintf("%s-docker-compose.yml", clusterSpec.Name)
	}

	if err := utils.WriteFile(outputFile, compose); err != nil {
		return fmt.Errorf("failed to write Docker Compose file: %w", err)
	}

	color.Green("✅ Docker Compose configuration exported successfully!")
	color.Cyan("📄 Generated services:")
	fmt.Printf("  • Masters: %d services\n", len(clusterSpec.MasterServers))
	fmt.Printf("  • Volumes: %d services\n", len(clusterSpec.VolumeServers))
	fmt.Printf("  • Filers: %d services\n", len(clusterSpec.FilerServers))

	color.Cyan("💾 Output saved to: %s", outputFile)
	color.Cyan("💡 Next steps:")
	fmt.Printf("  docker-compose -f %s up -d\n", outputFile)
	fmt.Println("  docker-compose ps")

	return nil
}

func runExportTerraform(configFile, outputFile, provider, region, instanceType string) error {
	color.Green("🏗️  Exporting to Terraform configuration")

	// Load cluster specification
	clusterSpec, err := loadClusterSpec(configFile)
	if err != nil {
		return fmt.Errorf("failed to load cluster configuration: %w", err)
	}

	color.Cyan("📋 Export Configuration:")
	fmt.Printf("Cluster: %s\n", clusterSpec.Name)
	fmt.Printf("Provider: %s\n", provider)
	fmt.Printf("Region: %s\n", region)
	fmt.Printf("Instance Type: %s\n", instanceType)

	// Generate Terraform content
	terraform := generateTerraform(clusterSpec, provider, region, instanceType)

	// Save to file
	if outputFile == "" {
		outputFile = fmt.Sprintf("%s-terraform.tf", clusterSpec.Name)
	}

	if err := utils.WriteFile(outputFile, terraform); err != nil {
		return fmt.Errorf("failed to write Terraform file: %w", err)
	}

	color.Green("✅ Terraform configuration exported successfully!")
	color.Cyan("📄 Generated resources:")
	fmt.Printf("  • Provider: %s\n", provider)
	fmt.Printf("  • Instances: %d\n", len(clusterSpec.MasterServers)+len(clusterSpec.VolumeServers)+len(clusterSpec.FilerServers))
	fmt.Println("  • Security Groups")
	fmt.Println("  • Load Balancer (if applicable)")

	color.Cyan("💾 Output saved to: %s", outputFile)
	color.Cyan("💡 Next steps:")
	fmt.Println("  terraform init")
	fmt.Println("  terraform plan")
	fmt.Println("  terraform apply")

	return nil
}

func runExportListFormats() error {
	color.Green("📦 Available Export Formats")

	formats := []map[string]string{
		{
			"format":      "kubernetes",
			"description": "Kubernetes manifests with deployments, services, and configmaps",
			"command":     "seaweed-up export kubernetes -f cluster.yaml",
		},
		{
			"format":      "docker-compose",
			"description": "Docker Compose file for container deployment",
			"command":     "seaweed-up export docker-compose -f cluster.yaml",
		},
		{
			"format":      "terraform",
			"description": "Terraform infrastructure-as-code for cloud deployment",
			"command":     "seaweed-up export terraform -f cluster.yaml --provider=aws",
		},
	}

	for _, format := range formats {
		color.Cyan("📄 %s", format["format"])
		fmt.Printf("   Description: %s\n", format["description"])
		fmt.Printf("   Usage: %s\n\n", format["command"])
	}

	color.Cyan("💡 General usage:")
	fmt.Println("  seaweed-up export <format> -f <cluster-config> -o <output-file>")

	return nil
}

// Helper functions for generating export content

func generateDockerCompose(cluster *spec.Specification, imageRepo, imageTag, networkName string) string {
	compose := fmt.Sprintf(`version: '3.8'

networks:
  %s:
    driver: bridge

services:
`, networkName)

	// Add master servers
	for i, master := range cluster.MasterServers {
		compose += fmt.Sprintf(`  seaweedfs-master-%d:
    image: %s:%s
    container_name: seaweedfs-master-%d
    command: ["weed", "master", "-port=%d", "-ip=seaweedfs-master-%d"]
    ports:
      - "%d:%d"
    networks:
      - %s
    restart: unless-stopped

`, i, imageRepo, imageTag, i, master.Port, i, master.Port, master.Port, networkName)
	}

	// Add volume servers
	for i, volume := range cluster.VolumeServers {
		compose += fmt.Sprintf(`  seaweedfs-volume-%d:
    image: %s:%s
    container_name: seaweedfs-volume-%d
    command: ["weed", "volume", "-port=%d", "-ip=seaweedfs-volume-%d", "-mserver=seaweedfs-master-0:%d"]
    ports:
      - "%d:%d"
    volumes:
      - seaweedfs-volume-%d-data:/data
    networks:
      - %s
    restart: unless-stopped
    depends_on:
      - seaweedfs-master-0

`, i, imageRepo, imageTag, i, volume.Port, i, cluster.MasterServers[0].Port, volume.Port, volume.Port, i, networkName)
	}

	// Add filer servers
	for i, filer := range cluster.FilerServers {
		compose += fmt.Sprintf(`  seaweedfs-filer-%d:
    image: %s:%s
    container_name: seaweedfs-filer-%d
    command: ["weed", "filer", "-port=%d", "-ip=seaweedfs-filer-%d", "-master=seaweedfs-master-0:%d"]
    ports:
      - "%d:%d"`, i, imageRepo, imageTag, i, filer.Port, i, cluster.MasterServers[0].Port, filer.Port, filer.Port)

		if filer.S3 {
			compose += fmt.Sprintf(`
      - "%d:%d"`, filer.S3Port, filer.S3Port)
		}

		compose += fmt.Sprintf(`
    networks:
      - %s
    restart: unless-stopped
    depends_on:
      - seaweedfs-master-0

`, networkName)
	}

	// Add volumes section
	compose += "\nvolumes:\n"
	for i := range cluster.VolumeServers {
		compose += fmt.Sprintf("  seaweedfs-volume-%d-data:\n", i)
	}

	return compose
}

func generateTerraform(cluster *spec.Specification, provider, region, instanceType string) string {
	terraform := fmt.Sprintf(`terraform {
  required_providers {
    %s = {
      source  = "hashicorp/%s"
      version = "~> 5.0"
    }
  }
}

provider "%s" {
  region = "%s"
}

# Security Group for SeaweedFS
resource "%s_security_group" "seaweedfs" {
  name_prefix = "%s-seaweedfs"
  description = "Security group for SeaweedFS cluster"

  ingress {
    from_port   = 9333
    to_port     = 9333
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  ingress {
    from_port   = 8080
    to_port     = 8080
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  ingress {
    from_port   = 8888
    to_port     = 8888
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  ingress {
    from_port   = 8333
    to_port     = 8333
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

`, provider, provider, provider, region, provider, cluster.Name)

	// Add master servers
	for i, master := range cluster.MasterServers {
		terraform += fmt.Sprintf(`# Master Server %d
resource "%s_instance" "seaweedfs_master_%d" {
  ami           = data.%s_ami.ubuntu.id
  instance_type = "%s"
  key_name      = var.key_name

  vpc_security_group_ids = [%s_security_group.seaweedfs.id]

  user_data = <<-EOF
              #!/bin/bash
              wget -O weed "https://github.com/seaweedfs/seaweedfs/releases/download/3.55/linux_amd64.tar.gz"
              tar -xzf linux_amd64.tar.gz
              ./weed master -port=%d
              EOF

  tags = {
    Name = "%s-master-%d"
    Type = "seaweedfs-master"
  }
}

`, i, provider, i, provider, instanceType, provider, master.Port, cluster.Name, i)
	}

	// Add volume servers
	for i, volume := range cluster.VolumeServers {
		terraform += fmt.Sprintf(`# Volume Server %d
resource "%s_instance" "seaweedfs_volume_%d" {
  ami           = data.%s_ami.ubuntu.id
  instance_type = "%s"
  key_name      = var.key_name

  vpc_security_group_ids = [%s_security_group.seaweedfs.id]

  user_data = <<-EOF
              #!/bin/bash
              wget -O weed "https://github.com/seaweedfs/seaweedfs/releases/download/3.55/linux_amd64.tar.gz"
              tar -xzf linux_amd64.tar.gz
              ./weed volume -port=%d -mserver=${%s_instance.seaweedfs_master_0.private_ip}:%d
              EOF

  tags = {
    Name = "%s-volume-%d"
    Type = "seaweedfs-volume"
  }
}

`, i, provider, i, provider, instanceType, provider, volume.Port, provider, cluster.MasterServers[0].Port, cluster.Name, i)
	}

	// Add data source and variables
	terraform += fmt.Sprintf(`
data "%s_ami" "ubuntu" {
  most_recent = true
  owners      = ["099720109477"] # Canonical

  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd/ubuntu-focal-20.04-amd64-server-*"]
  }
}

variable "key_name" {
  description = "Name of the AWS key pair"
  type        = string
}

output "master_ips" {
  value = [%s_instance.seaweedfs_master_0.public_ip]
}
`, provider, provider)

	return terraform
}
