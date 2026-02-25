package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	t.Parallel()

	yaml := `
server:
  addr: ":9090"
  read_timeout: 10s
database:
  dsn: ":memory:"
providers:
  - name: openai
    base_url: https://api.openai.com/v1
    api_key: sk-test
    models: [gpt-4o]
    priority: 1
routes:
  - model_alias: gpt-4o
    targets:
      - provider: openai
        model: gpt-4o
        priority: 1
    strategy: priority
`
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Server.Addr != ":9090" {
		t.Errorf("addr = %q, want %q", cfg.Server.Addr, ":9090")
	}
	if cfg.Database.DSN != ":memory:" {
		t.Errorf("dsn = %q, want %q", cfg.Database.DSN, ":memory:")
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("providers count = %d, want 1", len(cfg.Providers))
	}
	if cfg.Providers[0].Name != "openai" {
		t.Errorf("provider name = %q, want %q", cfg.Providers[0].Name, "openai")
	}
	if len(cfg.Routes) != 1 {
		t.Fatalf("routes count = %d, want 1", len(cfg.Routes))
	}
}

func TestExpandEnv(t *testing.T) {
	// Cannot use t.Parallel() with t.Setenv
	t.Setenv("TEST_API_KEY", "sk-secret-123")

	yaml := `api_key: ${TEST_API_KEY}`
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	// The env var should be expanded at the raw YAML level.
	// Since api_key is not a top-level Config field, we check via auth or providers.
	// Let's test expandEnv directly.
	result := expandEnv([]byte("key: ${TEST_API_KEY}"))
	if string(result) != "key: sk-secret-123" {
		t.Errorf("expandEnv = %q, want %q", string(result), "key: sk-secret-123")
	}

	_ = cfg
}

func TestLoadDefaults(t *testing.T) {
	t.Parallel()

	yaml := `{}`
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Server.Addr != ":8080" {
		t.Errorf("default addr = %q, want %q", cfg.Server.Addr, ":8080")
	}
	if cfg.Database.DSN != "gandalf.db" {
		t.Errorf("default dsn = %q, want %q", cfg.Database.DSN, "gandalf.db")
	}
}

func TestLoadHostingFields(t *testing.T) {
	t.Parallel()

	yamlData := `
providers:
  - name: azure-openai
    type: openai
    hosting: azure
    base_url: https://myinstance.openai.azure.com/openai/deployments/gpt-4o
    api_key: az-key
  - name: vertex-gemini
    type: gemini
    hosting: vertex
    region: us-central1
    project: my-project
    base_url: https://us-central1-aiplatform.googleapis.com
    auth:
      type: gcp_oauth
  - name: vertex-anthropic
    type: anthropic
    hosting: vertex
    region: europe-west1
    project: proj-2
    auth:
      type: gcp_oauth
  - name: bedrock-claude
    type: anthropic
    hosting: bedrock
    region: us-east-1
    base_url: https://bedrock-runtime.us-east-1.amazonaws.com
`
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	if err := os.WriteFile(path, []byte(yamlData), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(cfg.Providers) != 4 {
		t.Fatalf("providers = %d, want 4", len(cfg.Providers))
	}

	// Azure OpenAI
	az := cfg.Providers[0]
	if az.ResolvedHosting() != "azure" {
		t.Errorf("azure hosting = %q", az.ResolvedHosting())
	}
	if az.ResolvedAuthType() != "api_key" {
		t.Errorf("azure auth type = %q, want api_key", az.ResolvedAuthType())
	}
	if az.ResolvedAPIKey() != "az-key" {
		t.Errorf("azure api key = %q", az.ResolvedAPIKey())
	}

	// Vertex Gemini
	vg := cfg.Providers[1]
	if vg.ResolvedHosting() != "vertex" {
		t.Errorf("vertex-gemini hosting = %q", vg.ResolvedHosting())
	}
	if vg.ResolvedAuthType() != "gcp_oauth" {
		t.Errorf("vertex-gemini auth type = %q, want gcp_oauth", vg.ResolvedAuthType())
	}
	if vg.Region != "us-central1" {
		t.Errorf("vertex-gemini region = %q", vg.Region)
	}
	if vg.Project != "my-project" {
		t.Errorf("vertex-gemini project = %q", vg.Project)
	}

	// Vertex Anthropic
	va := cfg.Providers[2]
	if va.ResolvedHosting() != "vertex" {
		t.Errorf("vertex-anthropic hosting = %q", va.ResolvedHosting())
	}
	if va.Region != "europe-west1" {
		t.Errorf("vertex-anthropic region = %q", va.Region)
	}

	// Bedrock Anthropic
	br := cfg.Providers[3]
	if br.ResolvedHosting() != "bedrock" {
		t.Errorf("bedrock hosting = %q", br.ResolvedHosting())
	}
	if br.ResolvedAuthType() != "aws_sigv4" {
		t.Errorf("bedrock auth type = %q, want aws_sigv4", br.ResolvedAuthType())
	}
	if br.Region != "us-east-1" {
		t.Errorf("bedrock region = %q", br.Region)
	}
}

func TestProviderEntryResolvedAuthType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		entry   ProviderEntry
		wantAuth string
	}{
		{"no auth, no hosting", ProviderEntry{APIKey: "key"}, "api_key"},
		{"vertex infers gcp_oauth", ProviderEntry{Hosting: "vertex"}, "gcp_oauth"},
		{"bedrock infers aws_sigv4", ProviderEntry{Hosting: "bedrock"}, "aws_sigv4"},
		{"explicit overrides inference", ProviderEntry{Hosting: "vertex", Auth: &AuthEntry{Type: "api_key"}}, "api_key"},
		{"explicit gcp_oauth", ProviderEntry{Auth: &AuthEntry{Type: "gcp_oauth"}}, "gcp_oauth"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.entry.ResolvedAuthType(); got != tt.wantAuth {
				t.Errorf("ResolvedAuthType() = %q, want %q", got, tt.wantAuth)
			}
		})
	}
}

func TestProviderEntryResolvedAPIKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		entry   ProviderEntry
		wantKey string
	}{
		{"top-level key", ProviderEntry{APIKey: "top"}, "top"},
		{"auth key overrides", ProviderEntry{APIKey: "top", Auth: &AuthEntry{APIKey: "override"}}, "override"},
		{"auth empty falls back", ProviderEntry{APIKey: "top", Auth: &AuthEntry{}}, "top"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.entry.ResolvedAPIKey(); got != tt.wantKey {
				t.Errorf("ResolvedAPIKey() = %q, want %q", got, tt.wantKey)
			}
		})
	}
}
