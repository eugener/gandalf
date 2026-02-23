package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/dnscache"

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

	// Bootstrap from config
	ctx := context.Background()
	if err := config.Bootstrap(ctx, cfg, store); err != nil {
		return err
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
			continue
		}
		switch p.Name {
		case "openai":
			reg.Register(p.Name, openai.New(p.APIKey, p.BaseURL, dnsResolver))
		case "anthropic":
			reg.Register(p.Name, anthropic.New(p.APIKey, p.BaseURL, dnsResolver))
		case "gemini":
			reg.Register(p.Name, gemini.New(p.APIKey, p.BaseURL, dnsResolver))
		case "ollama":
			reg.Register(p.Name, ollama.New(p.APIKey, p.BaseURL, dnsResolver))
		default:
			slog.Warn("unknown provider, skipping", "name", p.Name)
		}
	}

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
