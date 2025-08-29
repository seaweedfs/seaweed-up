package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/operation"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/status"
	"github.com/seaweedfs/seaweed-up/pkg/component/registry"
	"github.com/seaweedfs/seaweed-up/pkg/monitoring/metrics"
	"github.com/seaweedfs/seaweed-up/pkg/plugins"
	"github.com/seaweedfs/seaweed-up/pkg/security/auth"
	"github.com/seaweedfs/seaweed-up/pkg/template"
)

// APIServer provides programmatic access to seaweed-up functionality
type APIServer struct {
	addr            string
	router          *mux.Router
	operationMgr    *operation.ClusterOperationManager
	statusCollector *status.StatusCollector
	registry        *registry.ComponentRegistry
	metricsStorage  metrics.MetricsStorage
	pluginManager   plugins.PluginManager
	templateManager *templates.TemplateManager
	authManager     *auth.AuthManager
	server          *http.Server
}

// NewAPIServer creates a new API server
func NewAPIServer(
	addr string,
	operationMgr *operation.ClusterOperationManager,
	statusCollector *status.StatusCollector,
	registry *registry.ComponentRegistry,
	metricsStorage metrics.MetricsStorage,
	pluginManager plugins.PluginManager,
	templateManager *templates.TemplateManager,
	authManager *auth.AuthManager,
) *APIServer {
	api := &APIServer{
		addr:            addr,
		router:          mux.NewRouter(),
		operationMgr:    operationMgr,
		statusCollector: statusCollector,
		registry:        registry,
		metricsStorage:  metricsStorage,
		pluginManager:   pluginManager,
		templateManager: templateManager,
		authManager:     authManager,
	}

	api.setupRoutes()
	return api
}

// Start starts the API server
func (api *APIServer) Start() error {
	api.server = &http.Server{
		Addr:         api.addr,
		Handler:      api.router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	fmt.Printf("🌐 API Server starting on %s\n", api.addr)
	return api.server.ListenAndServe()
}

// Stop stops the API server
func (api *APIServer) Stop() error {
	if api.server == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return api.server.Shutdown(ctx)
}

// setupRoutes configures API routes
func (api *APIServer) setupRoutes() {
	// API versioning
	v1 := api.router.PathPrefix("/api/v1").Subrouter()

	// Middleware
	v1.Use(api.loggingMiddleware)
	v1.Use(api.authMiddleware)
	v1.Use(api.corsMiddleware)

	// Health and info
	v1.HandleFunc("/health", api.healthHandler).Methods("GET")
	v1.HandleFunc("/info", api.infoHandler).Methods("GET")

	// Cluster operations
	clusters := v1.PathPrefix("/clusters").Subrouter()
	clusters.HandleFunc("", api.listClustersHandler).Methods("GET")
	clusters.HandleFunc("", api.createClusterHandler).Methods("POST")
	clusters.HandleFunc("/{name}", api.getClusterHandler).Methods("GET")
	clusters.HandleFunc("/{name}", api.updateClusterHandler).Methods("PUT")
	clusters.HandleFunc("/{name}", api.deleteClusterHandler).Methods("DELETE")
	clusters.HandleFunc("/{name}/status", api.getClusterStatusHandler).Methods("GET")
	clusters.HandleFunc("/{name}/upgrade", api.upgradeClusterHandler).Methods("POST")
	clusters.HandleFunc("/{name}/scale", api.scaleClusterHandler).Methods("POST")

	// Component management
	components := v1.PathPrefix("/components").Subrouter()
	components.HandleFunc("", api.listComponentsHandler).Methods("GET")
	components.HandleFunc("/install", api.installComponentHandler).Methods("POST")
	components.HandleFunc("/{name}", api.getComponentHandler).Methods("GET")
	components.HandleFunc("/{name}", api.uninstallComponentHandler).Methods("DELETE")

	// Metrics and monitoring
	metricsRouter := v1.PathPrefix("/metrics").Subrouter()
	metricsRouter.HandleFunc("", api.getMetricsHandler).Methods("GET")
	metricsRouter.HandleFunc("/query", api.queryMetricsHandler).Methods("POST")

	// Templates
	templatesRouter := v1.PathPrefix("/templates").Subrouter()
	templatesRouter.HandleFunc("", api.listTemplatesHandler).Methods("GET")
	templatesRouter.HandleFunc("/{name}", api.getTemplateHandler).Methods("GET")
	templatesRouter.HandleFunc("/{name}/generate", api.generateFromTemplateHandler).Methods("POST")

	// Plugins
	pluginsRouter := v1.PathPrefix("/plugins").Subrouter()
	pluginsRouter.HandleFunc("", api.listPluginsHandler).Methods("GET")
	pluginsRouter.HandleFunc("/{name}", api.getPluginHandler).Methods("GET")
	pluginsRouter.HandleFunc("/{name}/execute", api.executePluginHandler).Methods("POST")

	// Security
	security := v1.PathPrefix("/security").Subrouter()
	security.HandleFunc("/auth/status/{cluster}", api.getAuthStatusHandler).Methods("GET")
	security.HandleFunc("/auth/users", api.listUsersHandler).Methods("GET")
	security.HandleFunc("/auth/users", api.createUserHandler).Methods("POST")
}

// Request/Response types

type APIResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
	Message string      `json:"message,omitempty"`
}

type ClusterCreateRequest struct {
	Name   string               `json:"name"`
	Config *spec.Specification  `json:"config"`
	DryRun bool                 `json:"dry_run,omitempty"`
}

type ClusterUpgradeRequest struct {
	Version string `json:"version"`
	Staged  bool   `json:"staged,omitempty"`
}

type ClusterScaleRequest struct {
	AddVolume int `json:"add_volume,omitempty"`
	AddFiler  int `json:"add_filer,omitempty"`
}

type ComponentInstallRequest struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type MetricsQueryRequest struct {
	Metric    string            `json:"metric"`
	Labels    map[string]string `json:"labels,omitempty"`
	StartTime *time.Time        `json:"start_time,omitempty"`
	EndTime   *time.Time        `json:"end_time,omitempty"`
}

type TemplateGenerateRequest struct {
	Parameters map[string]interface{} `json:"parameters,omitempty"`
}

type PluginExecuteRequest struct {
	Operation string                 `json:"operation"`
	Params    map[string]interface{} `json:"params,omitempty"`
}

type UserCreateRequest struct {
	Username    string   `json:"username"`
	Password    string   `json:"password"`
	Roles       []string `json:"roles,omitempty"`
	Permissions []string `json:"permissions,omitempty"`
}

// Middleware

func (api *APIServer) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		fmt.Printf("%s %s %v\n", r.Method, r.URL.Path, time.Since(start))
	})
}

func (api *APIServer) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// For now, skip authentication on health endpoint
		if r.URL.Path == "/api/v1/health" {
			next.ServeHTTP(w, r)
			return
		}

		// Extract auth token from header
		token := r.Header.Get("Authorization")
		if token == "" {
			api.writeError(w, http.StatusUnauthorized, "Authentication required")
			return
		}

		// TODO: Validate token with auth manager
		// For now, accept any non-empty token
		
		next.ServeHTTP(w, r)
	})
}

func (api *APIServer) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Handler implementations

func (api *APIServer) healthHandler(w http.ResponseWriter, r *http.Request) {
	health := map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().UTC(),
		"version":   "2.0.0",
	}
	api.writeResponse(w, http.StatusOK, health)
}

func (api *APIServer) infoHandler(w http.ResponseWriter, r *http.Request) {
	info := map[string]interface{}{
		"name":        "seaweed-up-api",
		"version":     "2.0.0",
		"description": "SeaweedFS Cluster Management API",
		"endpoints": map[string]interface{}{
			"clusters":   "/api/v1/clusters",
			"components": "/api/v1/components",
			"metrics":    "/api/v1/metrics",
			"templates":  "/api/v1/templates",
			"plugins":    "/api/v1/plugins",
			"security":   "/api/v1/security",
		},
	}
	api.writeResponse(w, http.StatusOK, info)
}

func (api *APIServer) listClustersHandler(w http.ResponseWriter, r *http.Request) {
	// This would integrate with actual cluster storage
	clusters := []map[string]interface{}{
		{
			"name":   "demo-cluster",
			"status": "running",
			"nodes":  3,
		},
	}
	api.writeResponse(w, http.StatusOK, clusters)
}

func (api *APIServer) createClusterHandler(w http.ResponseWriter, r *http.Request) {
	var req ClusterCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Name == "" {
		api.writeError(w, http.StatusBadRequest, "Cluster name is required")
		return
	}

	if req.Config == nil {
		api.writeError(w, http.StatusBadRequest, "Cluster configuration is required")
		return
	}

	// Deploy cluster
	err := api.operationMgr.DeployCluster(req.Config)
	if err != nil {
		api.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Deployment failed: %v", err))
		return
	}

	result := map[string]interface{}{
		"name":    req.Name,
		"status":  "deployed",
		"message": "Cluster deployed successfully",
	}

	api.writeResponse(w, http.StatusCreated, result)
}

func (api *APIServer) getClusterStatusHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clusterName := vars["name"]

	// This would get actual cluster status
	status := map[string]interface{}{
		"name":   clusterName,
		"status": "running",
		"components": map[string]interface{}{
			"master": map[string]interface{}{
				"count":  1,
				"status": "healthy",
			},
			"volume": map[string]interface{}{
				"count":  2,
				"status": "healthy",
			},
			"filer": map[string]interface{}{
				"count":  1,
				"status": "healthy",
			},
		},
	}

	api.writeResponse(w, http.StatusOK, status)
}

func (api *APIServer) upgradeClusterHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clusterName := vars["name"]

	var req ClusterUpgradeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Version == "" {
		api.writeError(w, http.StatusBadRequest, "Version is required")
		return
	}

	// Perform upgrade
	err := api.operationMgr.UpgradeCluster(clusterName, req.Version)
	if err != nil {
		api.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Upgrade failed: %v", err))
		return
	}

	result := map[string]interface{}{
		"cluster": clusterName,
		"version": req.Version,
		"status":  "upgraded",
		"message": "Cluster upgraded successfully",
	}

	api.writeResponse(w, http.StatusOK, result)
}

func (api *APIServer) listComponentsHandler(w http.ResponseWriter, r *http.Request) {
	components, err := api.registry.ListComponents()
	if err != nil {
		api.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to list components: %v", err))
		return
	}

	api.writeResponse(w, http.StatusOK, components)
}

func (api *APIServer) listTemplatesHandler(w http.ResponseWriter, r *http.Request) {
	if api.templateManager == nil {
		api.writeError(w, http.StatusServiceUnavailable, "Template manager not available")
		return
	}

	templates := api.templateManager.ListTemplates()
	api.writeResponse(w, http.StatusOK, templates)
}

func (api *APIServer) listPluginsHandler(w http.ResponseWriter, r *http.Request) {
	if api.pluginManager == nil {
		api.writeError(w, http.StatusServiceUnavailable, "Plugin manager not available")
		return
	}

	plugins := api.pluginManager.ListLoadedPlugins()
	
	result := make([]map[string]interface{}, len(plugins))
	for i, plugin := range plugins {
		result[i] = map[string]interface{}{
			"name":        plugin.Name(),
			"version":     plugin.Version(),
			"description": plugin.Description(),
			"author":      plugin.Author(),
			"operations":  plugin.SupportedOperations(),
		}
	}

	api.writeResponse(w, http.StatusOK, result)
}

func (api *APIServer) getAuthStatusHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clusterName := vars["cluster"]

	if api.authManager == nil {
		api.writeError(w, http.StatusServiceUnavailable, "Auth manager not available")
		return
	}

	authConfig, err := api.authManager.GetAuthConfig(clusterName)
	if err != nil {
		api.writeError(w, http.StatusNotFound, fmt.Sprintf("Auth config not found: %v", err))
		return
	}

	result := map[string]interface{}{
		"cluster": clusterName,
		"method":  authConfig.Method,
		"enabled": authConfig.Enabled,
	}

	api.writeResponse(w, http.StatusOK, result)
}

// Utility methods

func (api *APIServer) writeResponse(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	response := APIResponse{
		Success: status < 400,
		Data:    data,
	}

	json.NewEncoder(w).Encode(response)
}

func (api *APIServer) writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	response := APIResponse{
		Success: false,
		Error:   message,
	}

	json.NewEncoder(w).Encode(response)
}

// Placeholder handlers for remaining endpoints
func (api *APIServer) getClusterHandler(w http.ResponseWriter, r *http.Request) {
	api.writeError(w, http.StatusNotImplemented, "Not implemented")
}

func (api *APIServer) updateClusterHandler(w http.ResponseWriter, r *http.Request) {
	api.writeError(w, http.StatusNotImplemented, "Not implemented")
}

func (api *APIServer) deleteClusterHandler(w http.ResponseWriter, r *http.Request) {
	api.writeError(w, http.StatusNotImplemented, "Not implemented")
}

func (api *APIServer) scaleClusterHandler(w http.ResponseWriter, r *http.Request) {
	api.writeError(w, http.StatusNotImplemented, "Not implemented")
}

func (api *APIServer) installComponentHandler(w http.ResponseWriter, r *http.Request) {
	api.writeError(w, http.StatusNotImplemented, "Not implemented")
}

func (api *APIServer) getComponentHandler(w http.ResponseWriter, r *http.Request) {
	api.writeError(w, http.StatusNotImplemented, "Not implemented")
}

func (api *APIServer) uninstallComponentHandler(w http.ResponseWriter, r *http.Request) {
	api.writeError(w, http.StatusNotImplemented, "Not implemented")
}

func (api *APIServer) getMetricsHandler(w http.ResponseWriter, r *http.Request) {
	api.writeError(w, http.StatusNotImplemented, "Not implemented")
}

func (api *APIServer) queryMetricsHandler(w http.ResponseWriter, r *http.Request) {
	api.writeError(w, http.StatusNotImplemented, "Not implemented")
}

func (api *APIServer) getTemplateHandler(w http.ResponseWriter, r *http.Request) {
	api.writeError(w, http.StatusNotImplemented, "Not implemented")
}

func (api *APIServer) generateFromTemplateHandler(w http.ResponseWriter, r *http.Request) {
	api.writeError(w, http.StatusNotImplemented, "Not implemented")
}

func (api *APIServer) getPluginHandler(w http.ResponseWriter, r *http.Request) {
	api.writeError(w, http.StatusNotImplemented, "Not implemented")
}

func (api *APIServer) executePluginHandler(w http.ResponseWriter, r *http.Request) {
	api.writeError(w, http.StatusNotImplemented, "Not implemented")
}

func (api *APIServer) listUsersHandler(w http.ResponseWriter, r *http.Request) {
	api.writeError(w, http.StatusNotImplemented, "Not implemented")
}

func (api *APIServer) createUserHandler(w http.ResponseWriter, r *http.Request) {
	api.writeError(w, http.StatusNotImplemented, "Not implemented")
}
