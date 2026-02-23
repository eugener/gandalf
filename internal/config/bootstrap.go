// Package config provides configuration loading and database bootstrapping.
package config

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/storage"
)

// Bootstrap seeds the database from the config file on first run.
func Bootstrap(ctx context.Context, cfg *Config, store storage.Store) error {
	// Seed providers
	for _, p := range cfg.Providers {
		pc := &gateway.ProviderConfig{
			ID:        p.Name,
			Name:      p.Name,
			BaseURL:   p.BaseURL,
			APIKeyEnc: "", // provider keys stay in memory only, never persisted
			Models:    p.Models,
			Priority:  p.Priority,
			Weight:    max(1, p.Weight),
			Enabled:   p.IsEnabled(),
			MaxRPS:    p.MaxRPS,
			TimeoutMs: max(5000, p.TimeoutMs),
		}
		existing, _ := store.GetProvider(ctx, pc.ID)
		if existing != nil {
			continue // already exists, skip
		}
		if err := store.CreateProvider(ctx, pc); err != nil {
			return err
		}
		slog.Info("bootstrapped provider", "name", pc.Name)
	}

	// Seed routes
	for _, r := range cfg.Routes {
		existing, _ := store.GetRouteByAlias(ctx, r.ModelAlias)
		if existing != nil {
			continue
		}
		targets, _ := json.Marshal(r.Targets)
		route := &gateway.Route{
			ID:         uuid.Must(uuid.NewV7()).String(),
			ModelAlias: r.ModelAlias,
			Targets:    targets,
			Strategy:   r.Strategy,
			CacheTTLs:  r.CacheTTLs,
		}
		if err := store.CreateRoute(ctx, route); err != nil {
			return err
		}
		slog.Info("bootstrapped route", "alias", r.ModelAlias)
	}

	// Seed API keys
	for _, k := range cfg.Keys {
		if k.Key == "" {
			continue
		}
		hash := gateway.HashKey(k.Key)

		existing, _ := store.GetKeyByHash(ctx, hash)
		if existing != nil {
			continue
		}

		prefix := k.Key
		if len(prefix) > 12 {
			prefix = prefix[:12]
		}

		role := k.Role
		if role == "" {
			role = "member"
		}

		key := &gateway.APIKey{
			ID:        uuid.Must(uuid.NewV7()).String(),
			KeyHash:   hash,
			KeyPrefix: prefix,
			OrgID:     k.OrgID,
			CreatedAt: time.Now().UTC(),
		}
		if err := store.CreateKey(ctx, key); err != nil {
			return err
		}
		slog.Info("bootstrapped api key", "name", k.Name, "prefix", prefix)
	}

	return nil
}

// GenerateAdminKey creates a random admin key and returns the plaintext.
func GenerateAdminKey() string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return gateway.APIKeyPrefix + base64.RawURLEncoding.EncodeToString(raw)
}
