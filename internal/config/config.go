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
	Server    ServerConfig    `yaml:"server"`
	Database  DatabaseConfig  `yaml:"database"`
	Auth      AuthConfig      `yaml:"auth"`
	Providers []ProviderEntry `yaml:"providers"`
	Routes    []RouteEntry    `yaml:"routes"`
	Keys      []KeyEntry      `yaml:"keys"`
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
	Name      string   `yaml:"name"`
	BaseURL   string   `yaml:"base_url"`
	APIKey    string   `yaml:"api_key"`
	Models    []string `yaml:"models"`
	Priority  int      `yaml:"priority"`
	Weight    int      `yaml:"weight"`
	Enabled   *bool    `yaml:"enabled"`
	MaxRPS    int      `yaml:"max_rps"`
	TimeoutMs int      `yaml:"timeout_ms"`
}

// IsEnabled reports whether the provider is enabled (defaults to true when nil).
func (p ProviderEntry) IsEnabled() bool {
	return p.Enabled == nil || *p.Enabled
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
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return cfg, nil
}
