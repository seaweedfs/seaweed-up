package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

// AuthMethod represents different authentication methods
type AuthMethod string

const (
	AuthMethodNone   AuthMethod = "none"
	AuthMethodJWT    AuthMethod = "jwt"
	AuthMethodBasic  AuthMethod = "basic"
	AuthMethodMTLS   AuthMethod = "mtls"
	AuthMethodAPIKey AuthMethod = "apikey"
)

// AuthConfig represents authentication configuration for a cluster
type AuthConfig struct {
	Method       AuthMethod                 `json:"method"`
	Enabled      bool                       `json:"enabled"`
	JWTConfig    *JWTConfig                 `json:"jwt_config,omitempty"`
	BasicConfig  *BasicAuthConfig           `json:"basic_config,omitempty"`
	MTLSConfig   *MTLSConfig                `json:"mtls_config,omitempty"`
	APIKeyConfig *APIKeyConfig              `json:"apikey_config,omitempty"`
	Settings     map[string]interface{}     `json:"settings,omitempty"`
}

// JWTConfig represents JWT authentication configuration
type JWTConfig struct {
	SecretKey          string `json:"secret_key"`
	Issuer             string `json:"issuer"`
	ExpirationMinutes  int    `json:"expiration_minutes"`
	RefreshEnabled     bool   `json:"refresh_enabled"`
	Algorithm          string `json:"algorithm"` // HS256, RS256, etc.
}

// BasicAuthConfig represents basic authentication configuration
type BasicAuthConfig struct {
	Users    map[string]string `json:"users"`    // username -> password hash
	Realm    string           `json:"realm"`
	HashType string           `json:"hash_type"` // sha256, bcrypt
}

// MTLSConfig represents mutual TLS configuration
type MTLSConfig struct {
	CACertPath     string   `json:"ca_cert_path"`
	ClientCertPath string   `json:"client_cert_path"`
	ClientKeyPath  string   `json:"client_key_path"`
	AllowedCNs     []string `json:"allowed_cns"`
	VerifyClient   bool     `json:"verify_client"`
}

// APIKeyConfig represents API key authentication configuration
type APIKeyConfig struct {
	Keys         map[string]APIKey `json:"keys"`         // key_id -> APIKey
	HeaderName   string            `json:"header_name"`  // X-API-Key
	QueryParam   string            `json:"query_param"`  // api_key
}

// APIKey represents an API key with metadata
type APIKey struct {
	ID          string    `json:"id"`
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Permissions []string  `json:"permissions"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	Active      bool      `json:"active"`
}

// User represents a system user with authentication info
type User struct {
	Username     string            `json:"username"`
	PasswordHash string            `json:"password_hash"`
	Roles        []string          `json:"roles"`
	Permissions  []string          `json:"permissions"`
	Metadata     map[string]string `json:"metadata"`
	CreatedAt    time.Time         `json:"created_at"`
	LastLogin    *time.Time        `json:"last_login,omitempty"`
	Active       bool              `json:"active"`
}

// AuthManager manages authentication for SeaweedFS clusters
type AuthManager struct {
	configDir string
	configs   map[string]*AuthConfig // cluster_name -> AuthConfig
	users     map[string]*User       // username -> User
}

// NewAuthManager creates a new authentication manager
func NewAuthManager(configDir string) *AuthManager {
	return &AuthManager{
		configDir: configDir,
		configs:   make(map[string]*AuthConfig),
		users:     make(map[string]*User),
	}
}

// InitializeAuth initializes authentication for a cluster
func (am *AuthManager) InitializeAuth(clusterName string, method AuthMethod) (*AuthConfig, error) {
	// Create config directory if it doesn't exist
	if err := os.MkdirAll(am.configDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create auth config directory: %w", err)
	}

	// Create authentication configuration based on method
	authConfig := &AuthConfig{
		Method:   method,
		Enabled:  true,
		Settings: make(map[string]interface{}),
	}

	switch method {
	case AuthMethodJWT:
		jwtConfig, err := am.generateJWTConfig(clusterName)
		if err != nil {
			return nil, fmt.Errorf("failed to generate JWT config: %w", err)
		}
		authConfig.JWTConfig = jwtConfig

	case AuthMethodBasic:
		basicConfig := &BasicAuthConfig{
			Users:    make(map[string]string),
			Realm:    fmt.Sprintf("SeaweedFS %s", clusterName),
			HashType: "sha256",
		}
		authConfig.BasicConfig = basicConfig

	case AuthMethodMTLS:
		mtlsConfig := &MTLSConfig{
			CACertPath:    filepath.Join(am.configDir, "..", "tls", "ca-cert.pem"),
			VerifyClient:  true,
			AllowedCNs:    []string{},
		}
		authConfig.MTLSConfig = mtlsConfig

	case AuthMethodAPIKey:
		apiKeyConfig := &APIKeyConfig{
			Keys:       make(map[string]APIKey),
			HeaderName: "X-API-Key",
			QueryParam: "api_key",
		}
		authConfig.APIKeyConfig = apiKeyConfig

	case AuthMethodNone:
		authConfig.Enabled = false

	default:
		return nil, fmt.Errorf("unsupported authentication method: %s", method)
	}

	// Save configuration
	if err := am.SaveAuthConfig(clusterName, authConfig); err != nil {
		return nil, fmt.Errorf("failed to save auth config: %w", err)
	}

	am.configs[clusterName] = authConfig
	return authConfig, nil
}

// generateJWTConfig generates a JWT configuration with a secure secret key
func (am *AuthManager) generateJWTConfig(clusterName string) (*JWTConfig, error) {
	// Generate a secure random secret key
	secretBytes := make([]byte, 64)
	if _, err := rand.Read(secretBytes); err != nil {
		return nil, fmt.Errorf("failed to generate random secret: %w", err)
	}
	
	secretKey := base64.StdEncoding.EncodeToString(secretBytes)

	return &JWTConfig{
		SecretKey:         secretKey,
		Issuer:            fmt.Sprintf("seaweed-up-%s", clusterName),
		ExpirationMinutes: 480, // 8 hours
		RefreshEnabled:    true,
		Algorithm:         "HS256",
	}, nil
}

// CreateUser creates a new user with the specified authentication method
func (am *AuthManager) CreateUser(username, password string, roles []string, permissions []string) (*User, error) {
	if _, exists := am.users[username]; exists {
		return nil, fmt.Errorf("user %s already exists", username)
	}

	// Hash the password
	passwordHash, err := am.hashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	user := &User{
		Username:     username,
		PasswordHash: passwordHash,
		Roles:        roles,
		Permissions:  permissions,
		Metadata:     make(map[string]string),
		CreatedAt:    time.Now(),
		Active:       true,
	}

	am.users[username] = user

	// Save users
	if err := am.saveUsers(); err != nil {
		return nil, fmt.Errorf("failed to save users: %w", err)
	}

	return user, nil
}

// CreateAPIKey creates a new API key
func (am *AuthManager) CreateAPIKey(clusterName, keyName string, permissions []string, expiresAt *time.Time) (*APIKey, error) {
	config, exists := am.configs[clusterName]
	if !exists || config.APIKeyConfig == nil {
		return nil, fmt.Errorf("API key authentication not configured for cluster %s", clusterName)
	}

	// Generate a secure API key
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return nil, fmt.Errorf("failed to generate API key: %w", err)
	}
	
	keyID := fmt.Sprintf("ak_%d", time.Now().Unix())
	keyValue := fmt.Sprintf("swfs_%s", base64.RawURLEncoding.EncodeToString(keyBytes))

	apiKey := APIKey{
		ID:          keyID,
		Key:         keyValue,
		Name:        keyName,
		Permissions: permissions,
		CreatedAt:   time.Now(),
		ExpiresAt:   expiresAt,
		Active:      true,
	}

	config.APIKeyConfig.Keys[keyID] = apiKey

	// Save configuration
	if err := am.SaveAuthConfig(clusterName, config); err != nil {
		return nil, fmt.Errorf("failed to save API key: %w", err)
	}

	return &apiKey, nil
}

// GenerateClusterAuthConfig generates authentication configuration for cluster components
func (am *AuthManager) GenerateClusterAuthConfig(cluster *spec.Specification, authConfig *AuthConfig) (map[string]interface{}, error) {
	config := make(map[string]interface{})

	switch authConfig.Method {
	case AuthMethodJWT:
		config["jwt"] = map[string]interface{}{
			"secret":      authConfig.JWTConfig.SecretKey,
			"signing_key": authConfig.JWTConfig.SecretKey,
		}

	case AuthMethodBasic:
		config["basic_auth"] = map[string]interface{}{
			"realm": authConfig.BasicConfig.Realm,
			"users": authConfig.BasicConfig.Users,
		}

	case AuthMethodMTLS:
		config["mtls"] = map[string]interface{}{
			"ca_cert":       authConfig.MTLSConfig.CACertPath,
			"verify_client": authConfig.MTLSConfig.VerifyClient,
			"allowed_cns":   authConfig.MTLSConfig.AllowedCNs,
		}

	case AuthMethodAPIKey:
		// Generate a simple key list for SeaweedFS
		var keys []string
		for _, apiKey := range authConfig.APIKeyConfig.Keys {
			if apiKey.Active && (apiKey.ExpiresAt == nil || apiKey.ExpiresAt.After(time.Now())) {
				keys = append(keys, apiKey.Key)
			}
		}
		config["api_keys"] = keys

	case AuthMethodNone:
		config["enabled"] = false
	}

	// Add cluster-specific settings
	config["cluster_name"] = cluster.Name
	config["auth_method"] = string(authConfig.Method)
	config["enabled"] = authConfig.Enabled

	return config, nil
}

// ValidateAuthConfig validates an authentication configuration
func (am *AuthManager) ValidateAuthConfig(authConfig *AuthConfig) error {
	if !authConfig.Enabled {
		return nil
	}

	switch authConfig.Method {
	case AuthMethodJWT:
		if authConfig.JWTConfig == nil {
			return fmt.Errorf("JWT configuration is required")
		}
		if authConfig.JWTConfig.SecretKey == "" {
			return fmt.Errorf("JWT secret key is required")
		}
		if authConfig.JWTConfig.ExpirationMinutes <= 0 {
			return fmt.Errorf("JWT expiration must be positive")
		}

	case AuthMethodBasic:
		if authConfig.BasicConfig == nil {
			return fmt.Errorf("Basic auth configuration is required")
		}
		if len(authConfig.BasicConfig.Users) == 0 {
			return fmt.Errorf("at least one user is required for basic auth")
		}

	case AuthMethodMTLS:
		if authConfig.MTLSConfig == nil {
			return fmt.Errorf("mTLS configuration is required")
		}
		if authConfig.MTLSConfig.CACertPath == "" {
			return fmt.Errorf("CA certificate path is required for mTLS")
		}
		// Check if CA certificate exists
		if _, err := os.Stat(authConfig.MTLSConfig.CACertPath); err != nil {
			return fmt.Errorf("CA certificate not found: %w", err)
		}

	case AuthMethodAPIKey:
		if authConfig.APIKeyConfig == nil {
			return fmt.Errorf("API key configuration is required")
		}
		if len(authConfig.APIKeyConfig.Keys) == 0 {
			return fmt.Errorf("at least one API key is required")
		}

	default:
		return fmt.Errorf("unsupported authentication method: %s", authConfig.Method)
	}

	return nil
}

// SaveAuthConfig saves authentication configuration to file
func (am *AuthManager) SaveAuthConfig(clusterName string, config *AuthConfig) error {
	configPath := filepath.Join(am.configDir, fmt.Sprintf("%s-auth.json", clusterName))
	
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal auth config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write auth config: %w", err)
	}

	return nil
}

// LoadAuthConfig loads authentication configuration from file
func (am *AuthManager) LoadAuthConfig(clusterName string) (*AuthConfig, error) {
	configPath := filepath.Join(am.configDir, fmt.Sprintf("%s-auth.json", clusterName))
	
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read auth config: %w", err)
	}

	var config AuthConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal auth config: %w", err)
	}

	am.configs[clusterName] = &config
	return &config, nil
}

// GetAuthConfig returns authentication configuration for a cluster
func (am *AuthManager) GetAuthConfig(clusterName string) (*AuthConfig, error) {
	if config, exists := am.configs[clusterName]; exists {
		return config, nil
	}

	// Try to load from file
	return am.LoadAuthConfig(clusterName)
}

// hashPassword hashes a password using SHA256
func (am *AuthManager) hashPassword(password string) (string, error) {
	hasher := sha256.New()
	hasher.Write([]byte(password))
	hash := hasher.Sum(nil)
	return hex.EncodeToString(hash), nil
}

// saveUsers saves user information to file
func (am *AuthManager) saveUsers() error {
	usersPath := filepath.Join(am.configDir, "users.json")
	
	data, err := json.MarshalIndent(am.users, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal users: %w", err)
	}

	if err := os.WriteFile(usersPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write users file: %w", err)
	}

	return nil
}

// LoadUsers loads user information from file
func (am *AuthManager) LoadUsers() error {
	usersPath := filepath.Join(am.configDir, "users.json")
	
	data, err := os.ReadFile(usersPath)
	if err != nil {
		// File doesn't exist, start with empty users
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read users file: %w", err)
	}

	if err := json.Unmarshal(data, &am.users); err != nil {
		return fmt.Errorf("failed to unmarshal users: %w", err)
	}

	return nil
}

// ListUsers returns all users
func (am *AuthManager) ListUsers() []*User {
	users := make([]*User, 0, len(am.users))
	for _, user := range am.users {
		users = append(users, user)
	}
	return users
}

// DeleteUser deletes a user
func (am *AuthManager) DeleteUser(username string) error {
	if _, exists := am.users[username]; !exists {
		return fmt.Errorf("user %s not found", username)
	}

	delete(am.users, username)
	return am.saveUsers()
}

// RevokeAPIKey revokes an API key
func (am *AuthManager) RevokeAPIKey(clusterName, keyID string) error {
	config, exists := am.configs[clusterName]
	if !exists || config.APIKeyConfig == nil {
		return fmt.Errorf("API key authentication not configured for cluster %s", clusterName)
	}

	if apiKey, exists := config.APIKeyConfig.Keys[keyID]; exists {
		apiKey.Active = false
		config.APIKeyConfig.Keys[keyID] = apiKey
		return am.SaveAuthConfig(clusterName, config)
	}

	return fmt.Errorf("API key %s not found", keyID)
}
