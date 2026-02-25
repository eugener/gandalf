// Package config handles YAML configuration loading with environment variable expansion.
package config

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"go.yaml.in/yaml/v3"
)

// Config is the top-level gateway configuration.
type Config struct {
	Server     ServerConfig     `yaml:"server"`
	Database   DatabaseConfig   `yaml:"database"`
	Auth       AuthConfig       `yaml:"auth"`
	RateLimits RateLimitConfig  `yaml:"rate_limits"`
	Cache      CacheConfig      `yaml:"cache"`
	Telemetry  TelemetryConfig  `yaml:"telemetry"`
	Providers  []ProviderEntry  `yaml:"providers"`
	Routes     []RouteEntry     `yaml:"routes"`
	Keys       []KeyEntry       `yaml:"keys"`
}

// TelemetryConfig holds observability settings.
type TelemetryConfig struct {
	Metrics MetricsConfig `yaml:"metrics"`
	Tracing TracingConfig `yaml:"tracing"`
}

// MetricsConfig controls Prometheus metrics.
type MetricsConfig struct {
	Enabled bool `yaml:"enabled"`
}

// TracingConfig controls OpenTelemetry tracing.
type TracingConfig struct {
	Enabled    bool    `yaml:"enabled"`
	Endpoint   string  `yaml:"endpoint"`    // OTLP gRPC endpoint
	SampleRate float64 `yaml:"sample_rate"` // 0.0 to 1.0
}

// RateLimitConfig holds default rate limiting settings.
type RateLimitConfig struct {
	DefaultRPM int64 `yaml:"default_rpm"` // default requests per minute (0 = unlimited)
	DefaultTPM int64 `yaml:"default_tpm"` // default tokens per minute (0 = unlimited)
}

// CacheConfig holds response cache settings.
type CacheConfig struct {
	Enabled    bool          `yaml:"enabled"`
	MaxSize    int           `yaml:"max_size"`
	DefaultTTL time.Duration `yaml:"default_ttl"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Addr            string        `yaml:"addr"`
	ReadTimeout     time.Duration `yaml:"read_timeout"`
	WriteTimeout    time.Duration `yaml:"write_timeout"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
}

// DatabaseConfig holds SQLite settings.
type DatabaseConfig struct {
	DSN string `yaml:"dsn"` // file path or ":memory:"
}

// AuthConfig holds authentication settings.
type AuthConfig struct {
	AdminKey string `yaml:"admin_key"` // bootstrap admin key (hashed on first use)
}

// ProviderEntry is a provider definition in the config file.
type ProviderEntry struct {
	Name      string     `yaml:"name"`
	Type      string     `yaml:"type"`
	BaseURL   string     `yaml:"base_url"`
	APIKey    string     `yaml:"api_key"`
	Models    []string   `yaml:"models"`
	Priority  int        `yaml:"priority"`
	Weight    int        `yaml:"weight"`
	Enabled   *bool      `yaml:"enabled"`
	MaxRPS    int        `yaml:"max_rps"`
	TimeoutMs int        `yaml:"timeout_ms"`
	Hosting   string     `yaml:"hosting"` // "", "azure", "vertex"
	Region    string     `yaml:"region"`  // GCP region for Vertex AI
	Project   string     `yaml:"project"` // GCP project ID for Vertex AI
	Auth      *AuthEntry `yaml:"auth"`    // explicit auth; inferred from api_key when absent
}

// AuthEntry configures provider authentication.
type AuthEntry struct {
	Type   string `yaml:"type"`    // "api_key", "gcp_oauth"
	APIKey string `yaml:"api_key"` // explicit key (overrides top-level api_key)
}

// IsEnabled reports whether the provider is enabled (defaults to true when nil).
func (p ProviderEntry) IsEnabled() bool {
	return p.Enabled == nil || *p.Enabled
}

// ResolvedType returns Type if set, otherwise falls back to Name for backward compatibility.
func (p ProviderEntry) ResolvedType() string {
	if p.Type != "" {
		return p.Type
	}
	return p.Name
}

// ResolvedHosting returns the normalized hosting mode ("", "azure", "vertex").
func (p ProviderEntry) ResolvedHosting() string {
	return p.Hosting
}

// ResolvedAuthType returns the auth type, inferring from context when Auth is nil.
// Returns "gcp_oauth" for Vertex hosting, "api_key" otherwise.
func (p ProviderEntry) ResolvedAuthType() string {
	if p.Auth != nil && p.Auth.Type != "" {
		return p.Auth.Type
	}
	if p.Hosting == "vertex" {
		return "gcp_oauth"
	}
	return "api_key"
}

// ResolvedAPIKey returns the API key, preferring Auth.APIKey over top-level APIKey.
func (p ProviderEntry) ResolvedAPIKey() string {
	if p.Auth != nil && p.Auth.APIKey != "" {
		return p.Auth.APIKey
	}
	return p.APIKey
}

// RouteEntry is a route definition in the config file.
type RouteEntry struct {
	ModelAlias string        `yaml:"model_alias"`
	Targets    []TargetEntry `yaml:"targets"`
	Strategy   string        `yaml:"strategy"`
	CacheTTLs  int           `yaml:"cache_ttl_s"`
}

// TargetEntry is a single route target.
type TargetEntry struct {
	Provider string `yaml:"provider" json:"provider_id"`
	Model    string `yaml:"model"    json:"model"`
	Priority int    `yaml:"priority" json:"priority"`
	Weight   int    `yaml:"weight"   json:"weight"`
}

// KeyEntry is an API key seed in the config file.
type KeyEntry struct {
	Name          string   `yaml:"name"`
	Key           string   `yaml:"key"` // plaintext, hashed on bootstrap
	OrgID         string   `yaml:"org_id"`
	AllowedModels []string `yaml:"allowed_models"`
	Role          string   `yaml:"role"`
}

var envPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

// expandEnv replaces ${VAR} patterns with environment variable values.
func expandEnv(data []byte) []byte {
	return envPattern.ReplaceAllFunc(data, func(match []byte) []byte {
		varName := string(match[2 : len(match)-1])
		if val, ok := os.LookupEnv(varName); ok {
			return []byte(val)
		}
		return match
	})
}

// Load reads and parses a YAML config file, expanding environment variables.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	data = expandEnv(data)

	cfg := &Config{
		Server: ServerConfig{
			Addr:            ":8080",
			ReadTimeout:     30 * time.Second,
			WriteTimeout:    120 * time.Second,
			ShutdownTimeout: 30 * time.Second,
		},
		Database: DatabaseConfig{
			DSN: "gandalf.db",
		},
		RateLimits: RateLimitConfig{
			DefaultRPM: 60,
			DefaultTPM: 100_000,
		},
		Cache: CacheConfig{
			Enabled:    true,
			MaxSize:    10_000,
			DefaultTTL: 5 * time.Minute,
		},
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return cfg, nil
}
