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
