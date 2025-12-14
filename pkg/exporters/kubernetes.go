package exporters

import (
	"context"
	"fmt"
	"strings"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/plugins"
	"gopkg.in/yaml.v3"
)

// KubernetesExporter exports SeaweedFS cluster configurations as Kubernetes manifests
type KubernetesExporter struct {
	namespace string
	options   KubernetesExportOptions
}

// KubernetesExportOptions contains configuration for Kubernetes export
type KubernetesExportOptions struct {
	Namespace           string            `yaml:"namespace"`
	StorageClass        string            `yaml:"storage_class,omitempty"`
	ImageRepository     string            `yaml:"image_repository"`
	ImageTag            string            `yaml:"image_tag"`
	ServiceType         string            `yaml:"service_type"` // ClusterIP, NodePort, LoadBalancer
	ResourceLimits      ResourceLimits    `yaml:"resource_limits,omitempty"`
	Labels              map[string]string `yaml:"labels,omitempty"`
	Annotations         map[string]string `yaml:"annotations,omitempty"`
	EnablePodMonitoring bool              `yaml:"enable_pod_monitoring"`
	EnableNetworkPolicy bool              `yaml:"enable_network_policy"`
	EnablePodSecurity   bool              `yaml:"enable_pod_security"`
}

// ResourceLimits defines resource limits for containers
type ResourceLimits struct {
	Master ResourceLimit `yaml:"master,omitempty"`
	Volume ResourceLimit `yaml:"volume,omitempty"`
	Filer  ResourceLimit `yaml:"filer,omitempty"`
}

// ResourceLimit defines CPU and memory limits
type ResourceLimit struct {
	CPU    string `yaml:"cpu,omitempty"`
	Memory string `yaml:"memory,omitempty"`
}

// NewKubernetesExporter creates a new Kubernetes exporter
func NewKubernetesExporter(namespace string, options KubernetesExportOptions) *KubernetesExporter {
	if namespace == "" {
		namespace = "seaweedfs"
	}
	
	if options.ImageRepository == "" {
		options.ImageRepository = "chrislusf/seaweedfs"
	}
	
	if options.ImageTag == "" {
		options.ImageTag = "latest"
	}
	
	if options.ServiceType == "" {
		options.ServiceType = "ClusterIP"
	}

	return &KubernetesExporter{
		namespace: namespace,
		options:   options,
	}
}

// Name returns the exporter name
func (k *KubernetesExporter) Name() string {
	return "kubernetes-exporter"
}

// Version returns the exporter version
func (k *KubernetesExporter) Version() string {
	return "1.0.0"
}

// Description returns the exporter description
func (k *KubernetesExporter) Description() string {
	return "Exports SeaweedFS cluster configurations as Kubernetes manifests"
}

// Author returns the exporter author
func (k *KubernetesExporter) Author() string {
	return "SeaweedFS Team"
}

// Initialize initializes the exporter
func (k *KubernetesExporter) Initialize(ctx context.Context, config map[string]interface{}) error {
	// Parse configuration if provided
	if config != nil {
		if namespace, ok := config["namespace"].(string); ok && namespace != "" {
			k.namespace = namespace
		}
	}
	return nil
}

// Validate validates the exporter
func (k *KubernetesExporter) Validate(ctx context.Context) error {
	if k.namespace == "" {
		return fmt.Errorf("namespace cannot be empty")
	}
	return nil
}

// Cleanup cleans up the exporter
func (k *KubernetesExporter) Cleanup(ctx context.Context) error {
	return nil
}

// SupportedOperations returns supported operations
func (k *KubernetesExporter) SupportedOperations() []plugins.OperationType {
	return []plugins.OperationType{plugins.OperationTypeExport}
}

// Execute executes the export operation
func (k *KubernetesExporter) Execute(ctx context.Context, operation plugins.OperationType, params map[string]interface{}) (*plugins.OperationResult, error) {
	if operation != plugins.OperationTypeExport {
		return nil, fmt.Errorf("unsupported operation: %s", operation)
	}

	// Extract cluster specification from parameters
	clusterData, ok := params["cluster"]
	if !ok {
		return nil, fmt.Errorf("cluster specification not provided")
	}

	cluster, ok := clusterData.(*spec.Specification)
	if !ok {
		return nil, fmt.Errorf("invalid cluster specification type")
	}

	// Generate Kubernetes manifests
	manifests, err := k.generateManifests(cluster)
	if err != nil {
		return nil, fmt.Errorf("failed to generate manifests: %w", err)
	}

	result := &plugins.OperationResult{
		Success: true,
		Message: "Kubernetes manifests generated successfully",
		Data: map[string]interface{}{
			"manifests": manifests,
			"namespace": k.namespace,
		},
	}

	return result, nil
}

// SupportedFormats returns supported export formats
func (k *KubernetesExporter) SupportedFormats() []plugins.ExportFormat {
	return []plugins.ExportFormat{plugins.ExportFormatKubernetes}
}

// Export exports cluster configuration to Kubernetes format
func (k *KubernetesExporter) Export(ctx context.Context, cluster *spec.Specification, format plugins.ExportFormat) ([]byte, error) {
	if format != plugins.ExportFormatKubernetes {
		return nil, fmt.Errorf("unsupported format: %s", format)
	}

	manifests, err := k.generateManifests(cluster)
	if err != nil {
		return nil, err
	}

	// Convert manifests to YAML
	var yamlOutput strings.Builder
	for i, manifest := range manifests {
		if i > 0 {
			yamlOutput.WriteString("---\n")
		}
		
		yamlBytes, err := yaml.Marshal(manifest)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal manifest: %w", err)
		}
		
		yamlOutput.Write(yamlBytes)
	}

	return []byte(yamlOutput.String()), nil
}

// generateManifests generates Kubernetes manifests for the cluster
func (k *KubernetesExporter) generateManifests(cluster *spec.Specification) ([]map[string]interface{}, error) {
	var manifests []map[string]interface{}

	// Generate namespace
	namespaceManifest := k.generateNamespace()
	manifests = append(manifests, namespaceManifest)

	// Generate ConfigMap for cluster configuration
	configMap := k.generateConfigMap(cluster)
	manifests = append(manifests, configMap)

	// Generate master server manifests
	for i, master := range cluster.MasterServers {
		masterManifests := k.generateMasterServer(cluster, master, i)
		manifests = append(manifests, masterManifests...)
	}

	// Generate volume server manifests
	for i, volume := range cluster.VolumeServers {
		volumeManifests := k.generateVolumeServer(cluster, volume, i)
		manifests = append(manifests, volumeManifests...)
	}

	// Generate filer server manifests
	for i, filer := range cluster.FilerServers {
		filerManifests := k.generateFilerServer(cluster, filer, i)
		manifests = append(manifests, filerManifests...)
	}

	// Generate monitoring manifests if enabled
	if k.options.EnablePodMonitoring {
		monitoringManifests := k.generateMonitoringResources(cluster)
		manifests = append(manifests, monitoringManifests...)
	}

	// Generate network policy if enabled
	if k.options.EnableNetworkPolicy {
		networkPolicy := k.generateNetworkPolicy(cluster)
		manifests = append(manifests, networkPolicy)
	}

	return manifests, nil
}

// generateNamespace generates namespace manifest
func (k *KubernetesExporter) generateNamespace() map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata": map[string]interface{}{
			"name": k.namespace,
			"labels": map[string]interface{}{
				"app.kubernetes.io/name":     "seaweedfs",
				"app.kubernetes.io/instance": k.namespace,
			},
		},
	}
}

// generateConfigMap generates ConfigMap for cluster configuration
func (k *KubernetesExporter) generateConfigMap(cluster *spec.Specification) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]interface{}{
			"name":      "seaweedfs-config",
			"namespace": k.namespace,
		},
		"data": map[string]interface{}{
			"cluster-name": cluster.Name,
			"replication":  cluster.GlobalOptions.Replication,
		},
	}
}

// generateMasterServer generates manifests for master server
func (k *KubernetesExporter) generateMasterServer(cluster *spec.Specification, master *spec.MasterServerSpec, index int) []map[string]interface{} {
	var manifests []map[string]interface{}

	name := fmt.Sprintf("seaweedfs-master-%d", index)
	
	// Deployment
	deployment := map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": k.namespace,
			"labels": map[string]interface{}{
				"app":                        "seaweedfs",
				"component":                  "master",
				"app.kubernetes.io/name":     "seaweedfs",
				"app.kubernetes.io/instance": k.namespace,
			},
		},
		"spec": map[string]interface{}{
			"replicas": 1,
			"selector": map[string]interface{}{
				"matchLabels": map[string]interface{}{
					"app":       "seaweedfs",
					"component": "master",
					"instance":  fmt.Sprintf("master-%d", index),
				},
			},
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": map[string]interface{}{
						"app":       "seaweedfs",
						"component": "master",
						"instance":  fmt.Sprintf("master-%d", index),
					},
				},
				"spec": map[string]interface{}{
					"containers": []map[string]interface{}{
						{
							"name":  "seaweedfs",
							"image": fmt.Sprintf("%s:%s", k.options.ImageRepository, k.options.ImageTag),
							"command": []string{
								"/usr/bin/weed",
								"master",
								fmt.Sprintf("-port=%d", master.Port),
								fmt.Sprintf("-peers=%s", strings.Join(master.Peers, ",")),
							},
							"ports": []map[string]interface{}{
								{
									"name":          "http",
									"containerPort": master.Port,
									"protocol":      "TCP",
								},
							},
							"env": []map[string]interface{}{
								{
									"name": "POD_NAMESPACE",
									"valueFrom": map[string]interface{}{
										"fieldRef": map[string]interface{}{
											"fieldPath": "metadata.namespace",
										},
									},
								},
								{
									"name": "POD_NAME",
									"valueFrom": map[string]interface{}{
										"fieldRef": map[string]interface{}{
											"fieldPath": "metadata.name",
										},
									},
								},
							},
							"livenessProbe": map[string]interface{}{
								"httpGet": map[string]interface{}{
									"path": "/cluster/status",
									"port": master.Port,
								},
								"initialDelaySeconds": 30,
								"timeoutSeconds":      10,
							},
							"readinessProbe": map[string]interface{}{
								"httpGet": map[string]interface{}{
									"path": "/cluster/status",
									"port": master.Port,
								},
								"initialDelaySeconds": 5,
								"timeoutSeconds":      3,
							},
						},
					},
				},
			},
		},
	}

	// Add resource limits if specified
	if k.options.ResourceLimits.Master.CPU != "" || k.options.ResourceLimits.Master.Memory != "" {
		container := deployment["spec"].(map[string]interface{})["template"].(map[string]interface{})["spec"].(map[string]interface{})["containers"].([]map[string]interface{})[0]
		
		resources := map[string]interface{}{
			"limits": map[string]interface{}{},
		}
		
		if k.options.ResourceLimits.Master.CPU != "" {
			resources["limits"].(map[string]interface{})["cpu"] = k.options.ResourceLimits.Master.CPU
		}
		if k.options.ResourceLimits.Master.Memory != "" {
			resources["limits"].(map[string]interface{})["memory"] = k.options.ResourceLimits.Master.Memory
		}
		
		container["resources"] = resources
	}

	manifests = append(manifests, deployment)

	// Service
	service := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": k.namespace,
			"labels": map[string]interface{}{
				"app":                        "seaweedfs",
				"component":                  "master",
				"app.kubernetes.io/name":     "seaweedfs",
				"app.kubernetes.io/instance": k.namespace,
			},
		},
		"spec": map[string]interface{}{
			"type": k.options.ServiceType,
			"ports": []map[string]interface{}{
				{
					"name":       "http",
					"port":       master.Port,
					"targetPort": master.Port,
					"protocol":   "TCP",
				},
			},
			"selector": map[string]interface{}{
				"app":       "seaweedfs",
				"component": "master",
				"instance":  fmt.Sprintf("master-%d", index),
			},
		},
	}

	manifests = append(manifests, service)

	return manifests
}

// generateVolumeServer generates manifests for volume server
func (k *KubernetesExporter) generateVolumeServer(cluster *spec.Specification, volume *spec.VolumeServerSpec, index int) []map[string]interface{} {
	var manifests []map[string]interface{}

	name := fmt.Sprintf("seaweedfs-volume-%d", index)

	// StatefulSet for persistent storage
	statefulSet := map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "StatefulSet",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": k.namespace,
			"labels": map[string]interface{}{
				"app":                        "seaweedfs",
				"component":                  "volume",
				"app.kubernetes.io/name":     "seaweedfs",
				"app.kubernetes.io/instance": k.namespace,
			},
		},
		"spec": map[string]interface{}{
			"serviceName": name,
			"replicas":    1,
			"selector": map[string]interface{}{
				"matchLabels": map[string]interface{}{
					"app":       "seaweedfs",
					"component": "volume",
					"instance":  fmt.Sprintf("volume-%d", index),
				},
			},
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": map[string]interface{}{
						"app":       "seaweedfs",
						"component": "volume",
						"instance":  fmt.Sprintf("volume-%d", index),
					},
				},
				"spec": map[string]interface{}{
					"containers": []map[string]interface{}{
						{
							"name":  "seaweedfs",
							"image": fmt.Sprintf("%s:%s", k.options.ImageRepository, k.options.ImageTag),
							"command": []string{
								"/usr/bin/weed",
								"volume",
								fmt.Sprintf("-port=%d", volume.Port),
								fmt.Sprintf("-mserver=%s", strings.Join(volume.Masters, ",")),
								"-dir=/data",
							},
							"ports": []map[string]interface{}{
								{
									"name":          "http",
									"containerPort": volume.Port,
									"protocol":      "TCP",
								},
							},
							"volumeMounts": []map[string]interface{}{
								{
									"name":      "data",
									"mountPath": "/data",
								},
							},
						},
					},
				},
			},
			"volumeClaimTemplates": []map[string]interface{}{
				{
					"metadata": map[string]interface{}{
						"name": "data",
					},
					"spec": map[string]interface{}{
						"accessModes": []string{"ReadWriteOnce"},
						"resources": map[string]interface{}{
							"requests": map[string]interface{}{
								"storage": "10Gi",
							},
						},
					},
				},
			},
		},
	}

	// Add storage class if specified
	if k.options.StorageClass != "" {
		volumeClaimTemplate := statefulSet["spec"].(map[string]interface{})["volumeClaimTemplates"].([]map[string]interface{})[0]
		volumeClaimTemplate["spec"].(map[string]interface{})["storageClassName"] = k.options.StorageClass
	}

	manifests = append(manifests, statefulSet)

	// Service
	service := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": k.namespace,
		},
		"spec": map[string]interface{}{
			"type": "ClusterIP",
			"ports": []map[string]interface{}{
				{
					"name":       "http",
					"port":       volume.Port,
					"targetPort": volume.Port,
				},
			},
			"selector": map[string]interface{}{
				"app":       "seaweedfs",
				"component": "volume",
				"instance":  fmt.Sprintf("volume-%d", index),
			},
		},
	}

	manifests = append(manifests, service)

	return manifests
}

// generateFilerServer generates manifests for filer server
func (k *KubernetesExporter) generateFilerServer(cluster *spec.Specification, filer *spec.FilerServerSpec, index int) []map[string]interface{} {
	var manifests []map[string]interface{}

	name := fmt.Sprintf("seaweedfs-filer-%d", index)

	// Deployment
	deployment := map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": k.namespace,
		},
		"spec": map[string]interface{}{
			"replicas": 1,
			"selector": map[string]interface{}{
				"matchLabels": map[string]interface{}{
					"app":       "seaweedfs",
					"component": "filer",
					"instance":  fmt.Sprintf("filer-%d", index),
				},
			},
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": map[string]interface{}{
						"app":       "seaweedfs",
						"component": "filer",
						"instance":  fmt.Sprintf("filer-%d", index),
					},
				},
				"spec": map[string]interface{}{
					"containers": []map[string]interface{}{
						{
							"name":  "seaweedfs",
							"image": fmt.Sprintf("%s:%s", k.options.ImageRepository, k.options.ImageTag),
							"command": []string{
								"/usr/bin/weed",
								"filer",
								fmt.Sprintf("-port=%d", filer.Port),
								fmt.Sprintf("-master=%s", strings.Join(filer.Masters, ",")),
							},
							"ports": []map[string]interface{}{
								{
									"name":          "http",
									"containerPort": filer.Port,
								},
							},
						},
					},
				},
			},
		},
	}

	manifests = append(manifests, deployment)

	// Service
	service := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": k.namespace,
		},
		"spec": map[string]interface{}{
			"type": k.options.ServiceType,
			"ports": []map[string]interface{}{
				{
					"name":       "http",
					"port":       filer.Port,
					"targetPort": filer.Port,
				},
			},
			"selector": map[string]interface{}{
				"app":       "seaweedfs",
				"component": "filer",
				"instance":  fmt.Sprintf("filer-%d", index),
			},
		},
	}

	// Add S3 port if enabled
	if filer.S3 {
		service["spec"].(map[string]interface{})["ports"] = append(
			service["spec"].(map[string]interface{})["ports"].([]map[string]interface{}),
			map[string]interface{}{
				"name":       "s3",
				"port":       filer.S3Port,
				"targetPort": filer.S3Port,
			},
		)

		// Add S3 command arguments
		container := deployment["spec"].(map[string]interface{})["template"].(map[string]interface{})["spec"].(map[string]interface{})["containers"].([]map[string]interface{})[0]
		container["command"] = append(container["command"].([]string), fmt.Sprintf("-s3.port=%d", filer.S3Port))

		// IAM is embedded in S3 by default. Only add -iam=false if explicitly disabled
		if filer.IAM != nil && !*filer.IAM {
			container["command"] = append(container["command"].([]string), "-iam=false")
		}
		// Note: IAM API is accessible on the same port as S3 (S3Port) when embedded

		// Add S3 port to container ports
		container["ports"] = append(container["ports"].([]map[string]interface{}), map[string]interface{}{
			"name":          "s3",
			"containerPort": filer.S3Port,
		})
	}

	manifests = append(manifests, service)

	return manifests
}

// generateMonitoringResources generates monitoring resources
func (k *KubernetesExporter) generateMonitoringResources(cluster *spec.Specification) []map[string]interface{} {
	var manifests []map[string]interface{}

	// ServiceMonitor for Prometheus
	serviceMonitor := map[string]interface{}{
		"apiVersion": "monitoring.coreos.com/v1",
		"kind":       "ServiceMonitor",
		"metadata": map[string]interface{}{
			"name":      "seaweedfs-monitoring",
			"namespace": k.namespace,
		},
		"spec": map[string]interface{}{
			"selector": map[string]interface{}{
				"matchLabels": map[string]interface{}{
					"app": "seaweedfs",
				},
			},
			"endpoints": []map[string]interface{}{
				{
					"port":     "http",
					"interval": "30s",
					"path":     "/metrics",
				},
			},
		},
	}

	manifests = append(manifests, serviceMonitor)

	return manifests
}

// generateNetworkPolicy generates network policy
func (k *KubernetesExporter) generateNetworkPolicy(cluster *spec.Specification) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "NetworkPolicy",
		"metadata": map[string]interface{}{
			"name":      "seaweedfs-network-policy",
			"namespace": k.namespace,
		},
		"spec": map[string]interface{}{
			"podSelector": map[string]interface{}{
				"matchLabels": map[string]interface{}{
					"app": "seaweedfs",
				},
			},
			"policyTypes": []string{"Ingress", "Egress"},
			"ingress": []map[string]interface{}{
				{
					"from": []map[string]interface{}{
						{
							"podSelector": map[string]interface{}{
								"matchLabels": map[string]interface{}{
									"app": "seaweedfs",
								},
							},
						},
					},
				},
			},
			"egress": []map[string]interface{}{
				{
					"to": []map[string]interface{}{
						{
							"podSelector": map[string]interface{}{
								"matchLabels": map[string]interface{}{
									"app": "seaweedfs",
								},
							},
						},
					},
				},
			},
		},
	}
}
