package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/rs/dnscache"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/app"
	"github.com/eugener/gandalf/internal/auth"
	"github.com/eugener/gandalf/internal/config"
	"github.com/eugener/gandalf/internal/provider"
	"github.com/eugener/gandalf/internal/provider/anthropic"
	"github.com/eugener/gandalf/internal/provider/gemini"
	"github.com/eugener/gandalf/internal/provider/ollama"
	"github.com/eugener/gandalf/internal/provider/openai"
	"github.com/eugener/gandalf/internal/server"
	"github.com/eugener/gandalf/internal/storage/sqlite"
)

func run(configPath string) error {
	// Load config
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	slog.Info("starting gandalf", "version", version, "addr", cfg.Server.Addr)

	// Open database
	store, err := sqlite.New(cfg.Database.DSN)
	if err != nil {
		return err
	}
	defer store.Close()

	slog.Info("database opened", "dsn", cfg.Database.DSN)

	// Bootstrap from config
	ctx := context.Background()
	if err := config.Bootstrap(ctx, cfg, store); err != nil {
		return err
	}

	// Log seeded API keys (names only, never log key material).
	for _, k := range cfg.Keys {
		if k.Key == "" {
			slog.Warn("api key empty, skipped", "name", k.Name)
			continue
		}
		valid := strings.HasPrefix(k.Key, gateway.APIKeyPrefix)
		slog.Info("api key configured", "name", k.Name, "valid_prefix", valid)
	}

	// Shared DNS cache for all provider HTTP clients.
	dnsResolver := &dnscache.Resolver{}
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for range t.C {
			dnsResolver.Refresh(true)
		}
	}()

	// Register providers
	reg := provider.NewRegistry()
	for _, p := range cfg.Providers {
		if !p.IsEnabled() {
			slog.Info("provider skipped (disabled)", "name", p.Name)
			continue
		}
		var prov gateway.Provider
		switch p.Name {
		case "openai":
			prov = openai.New(p.APIKey, p.BaseURL, dnsResolver)
		case "anthropic":
			prov = anthropic.New(p.APIKey, p.BaseURL, dnsResolver)
		case "gemini":
			prov = gemini.New(p.APIKey, p.BaseURL, dnsResolver)
		case "ollama":
			prov = ollama.New(p.APIKey, p.BaseURL, dnsResolver)
		default:
			slog.Warn("unknown provider, skipping", "name", p.Name)
			continue
		}
		_, hasNative := prov.(gateway.NativeProxy)
		reg.Register(p.Name, prov)
		slog.Info("provider registered", "name", p.Name, "native_proxy", hasNative)
	}

	for _, r := range cfg.Routes {
		targets := make([]string, len(r.Targets))
		for i, t := range r.Targets {
			targets[i] = t.Provider + "/" + t.Model
		}
		slog.Info("route configured", "alias", r.ModelAlias, "targets", targets)
	}
	slog.Info("server timeouts",
		"read", cfg.Server.ReadTimeout,
		"write", cfg.Server.WriteTimeout,
		"shutdown", cfg.Server.ShutdownTimeout,
	)

	// Wire services
	apiKeyAuth, err := auth.NewAPIKeyAuth(store)
	if err != nil {
		return err
	}

	routerSvc := app.NewRouterService(store)
	proxySvc := app.NewProxyService(reg, routerSvc)

	keys := app.NewKeyManager(store)

	// Create HTTP server
	handler := server.New(server.Deps{
		Auth:       apiKeyAuth,
		Proxy:      proxySvc,
		Providers:  reg,
		Router:     routerSvc,
		Keys:       keys,
		ReadyCheck: store.Ping,
	})

	srv := &http.Server{
		Addr:         cfg.Server.Addr,
		Handler:      handler,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	// Graceful shutdown
	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	slog.Info("universal API enabled",
		"endpoints", []string{
			"POST /v1/chat/completions",
			"POST /v1/embeddings",
			"GET  /v1/models",
		},
	)
	slog.Info("gandalf ready", "addr", cfg.Server.Addr)

	// Wait for signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	select {
	case sig := <-sigCh:
		slog.Info("shutting down", "signal", sig)
	case err := <-errCh:
		return err
	}

	// Shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}

	slog.Info("gandalf stopped")
	return nil
}
