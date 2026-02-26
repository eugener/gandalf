package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/dnscache"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/app"
	"github.com/eugener/gandalf/internal/auth"
	"github.com/eugener/gandalf/internal/circuitbreaker"
	"github.com/eugener/gandalf/internal/cache"
	"github.com/eugener/gandalf/internal/cloudauth"
	"github.com/eugener/gandalf/internal/config"
	"github.com/eugener/gandalf/internal/provider"
	"github.com/eugener/gandalf/internal/provider/anthropic"
	"github.com/eugener/gandalf/internal/provider/gemini"
	"github.com/eugener/gandalf/internal/provider/ollama"
	"github.com/eugener/gandalf/internal/provider/openai"
	"github.com/eugener/gandalf/internal/ratelimit"
	"github.com/eugener/gandalf/internal/server"
	"github.com/eugener/gandalf/internal/storage/sqlite"
	"github.com/eugener/gandalf/internal/telemetry"
	"github.com/eugener/gandalf/internal/tokencount"
	"github.com/eugener/gandalf/internal/worker"
	"go.opentelemetry.io/otel/trace"
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
	defer store.Close() //nolint:errcheck // best-effort cleanup

	dsnLog := cfg.Database.DSN
	if i := strings.IndexByte(dsnLog, '?'); i >= 0 {
		dsnLog = dsnLog[:i]
	}
	slog.Info("database opened", "dsn", dsnLog)

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
	// The refresh goroutine is started later with workerCtx for clean shutdown.
	dnsResolver := &dnscache.Resolver{}

	// Register providers
	reg := provider.NewRegistry()
	for _, p := range cfg.Providers {
		if !p.IsEnabled() {
			slog.Info("provider skipped (disabled)", "name", p.Name)
			continue
		}

		// Build HTTP client with auth transport chain.
		client, err := buildProviderClient(ctx, p, dnsResolver)
		if err != nil {
			return fmt.Errorf("provider %q: %w", p.Name, err)
		}

		var prov gateway.Provider
		switch p.ResolvedType() {
		case "openai":
			if h := p.ResolvedHosting(); h == "azure" {
				prov = openai.NewWithHosting(p.Name, p.BaseURL, client, h)
			} else {
				prov = openai.New(p.Name, p.BaseURL, client)
			}
		case "anthropic":
			if h := p.ResolvedHosting(); h == "vertex" || h == "bedrock" {
				prov = anthropic.NewWithHosting(p.Name, p.BaseURL, client, h, p.Region, p.Project)
			} else {
				prov = anthropic.New(p.Name, p.BaseURL, client)
			}
		case "gemini":
			if h := p.ResolvedHosting(); h == "vertex" {
				prov = gemini.NewWithHosting(p.Name, p.BaseURL, client, h, p.Region, p.Project)
			} else {
				prov = gemini.New(p.Name, p.BaseURL, client)
			}
		case "ollama":
			prov = ollama.New(p.Name, p.BaseURL, client)
		default:
			slog.Warn("unknown provider type, skipping", "name", p.Name, "type", p.ResolvedType())
			continue
		}
		_, hasNative := prov.(gateway.NativeProxy)
		reg.Register(p.Name, prov)
		slog.Info("provider registered",
			"name", p.Name,
			"type", p.ResolvedType(),
			"hosting", p.ResolvedHosting(),
			"auth", p.ResolvedAuthType(),
			"native_proxy", hasNative,
		)
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

	// Prometheus metrics.
	var metrics *telemetry.Metrics
	var metricsHandler http.Handler
	if cfg.Telemetry.Metrics.Enabled {
		promRegistry := prometheus.NewRegistry()
		promRegistry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
		promRegistry.MustRegister(collectors.NewGoCollector())
		metrics = telemetry.NewMetrics(promRegistry)
		metricsHandler = promhttp.HandlerFor(promRegistry, promhttp.HandlerOpts{})
		slog.Info("prometheus metrics enabled")
	}

	// OpenTelemetry tracing.
	var tracer trace.Tracer
	var tracingShutdown func(context.Context) error
	if cfg.Telemetry.Tracing.Enabled {
		endpoint := cfg.Telemetry.Tracing.Endpoint
		if endpoint == "" {
			endpoint = "localhost:4317"
		}
		sampleRate := cfg.Telemetry.Tracing.SampleRate
		if sampleRate == 0 {
			sampleRate = 0.1
		}
		shutdown, err := telemetry.SetupTracing(ctx, endpoint, sampleRate)
		if err != nil {
			slog.Warn("tracing setup failed, continuing without tracing", "error", err)
		} else {
			tracingShutdown = shutdown
			tracer = telemetry.Tracer("gandalf/server")
			slog.Info("opentelemetry tracing enabled",
				"endpoint", endpoint,
				"sample_rate", sampleRate,
			)
		}
	}

	// Wire services
	apiKeyAuth, err := auth.NewAPIKeyAuth(store)
	if err != nil {
		return err
	}

	routerSvc := app.NewRouterService(store)

	// Circuit breaker.
	var breakers *circuitbreaker.Registry
	if cfg.CircuitBreaker.Enabled {
		cbCfg := circuitbreaker.Config{
			ErrorThreshold: cfg.CircuitBreaker.ErrorThreshold,
			MinSamples:     cfg.CircuitBreaker.MinSamples,
			WindowSeconds:  cfg.CircuitBreaker.WindowSeconds,
			OpenTimeout:    cfg.CircuitBreaker.OpenTimeout,
		}
		if cbCfg.ErrorThreshold == 0 {
			cbCfg.ErrorThreshold = 0.30
		}
		if cbCfg.MinSamples == 0 {
			cbCfg.MinSamples = 10
		}
		if cbCfg.WindowSeconds == 0 {
			cbCfg.WindowSeconds = 60
		}
		if cbCfg.OpenTimeout == 0 {
			cbCfg.OpenTimeout = 30 * time.Second
		}
		breakers = circuitbreaker.NewRegistry(cbCfg)
		slog.Info("circuit breaker enabled",
			"threshold", cbCfg.ErrorThreshold,
			"min_samples", cbCfg.MinSamples,
			"window_seconds", cbCfg.WindowSeconds,
			"open_timeout", cbCfg.OpenTimeout,
		)
	}

	proxySvc := app.NewProxyService(reg, routerSvc, tracer, breakers)
	keys := app.NewKeyManager(store)

	// Usage recorder (async batch flush to DB).
	usageRecorder := worker.NewUsageRecorder(store)

	// Rate limiter.
	rateLimiter := ratelimit.NewRegistry()
	slog.Info("rate limits configured",
		"default_rpm", cfg.RateLimits.DefaultRPM,
		"default_tpm", cfg.RateLimits.DefaultTPM,
	)

	// Token counter.
	tokenCounter := tokencount.NewCounter()

	// Response cache.
	var responseCache server.Cache
	if cfg.Cache.Enabled {
		mc, cacheErr := cache.NewMemory(cfg.Cache.MaxSize, cfg.Cache.DefaultTTL)
		if cacheErr != nil {
			return cacheErr
		}
		responseCache = mc
		slog.Info("response cache enabled",
			"max_size", cfg.Cache.MaxSize,
			"default_ttl", cfg.Cache.DefaultTTL,
		)
	}

	// Quota tracker.
	quotaTracker := ratelimit.NewQuotaTracker()

	// Workers.
	workers := []worker.Worker{usageRecorder}
	workers = append(workers, worker.NewQuotaSyncWorkerWithBudgets(quotaTracker, store, store))
	workers = append(workers, worker.NewUsageRollupWorker(store))

	runner := worker.NewRunner(workers...)

	// Create HTTP server
	handler := server.New(server.Deps{
		Auth:         apiKeyAuth,
		Proxy:        proxySvc,
		Providers:    reg,
		Router:       routerSvc,
		Keys:         keys,
		Store:        store,
		ReadyCheck:   store.Ping,
		Usage:        usageRecorder,
		RateLimiter:  rateLimiter,
		TokenCounter: tokenCounter,
		Cache:          responseCache,
		Quota:          quotaTracker,
		KeyInvalidator: apiKeyAuth,
		Metrics:        metrics,
		MetricsHandler: metricsHandler,
		Tracer:         tracer,
		DefaultRPM:     cfg.RateLimits.DefaultRPM,
		DefaultTPM:     cfg.RateLimits.DefaultTPM,
	})

	srv := &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           handler,
		ReadTimeout:       cfg.Server.ReadTimeout,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      cfg.Server.WriteTimeout,
		IdleTimeout:       120 * time.Second,
	}

	// Start background workers.
	workerCtx, workerCancel := context.WithCancel(context.Background())
	workerDone := make(chan error, 1)
	go func() {
		workerDone <- runner.Run(workerCtx)
	}()

	// Periodic DNS cache refresh (tied to workerCtx for clean shutdown).
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-t.C:
				dnsResolver.Refresh(true)
			}
		}
	}()

	// Periodic eviction of stale rate limiters.
	go func() {
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-t.C:
				if n := rateLimiter.EvictStale(time.Now().Add(-1 * time.Hour)); n > 0 {
					slog.Info("rate limiter eviction", "evicted", n)
				}
			}
		}
	}()

	// Periodic eviction of stale circuit breakers.
	if breakers != nil {
		go func() {
			t := time.NewTicker(10 * time.Minute)
			defer t.Stop()
			for {
				select {
				case <-workerCtx.Done():
					return
				case <-t.C:
					if n := breakers.EvictStale(time.Now().Add(-1 * time.Hour)); n > 0 {
						slog.Info("circuit breaker eviction", "evicted", n)
					}
				}
			}
		}()
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
		workerCancel()
		return err
	}

	// Shutdown HTTP first, then workers (so in-flight requests finish recording).
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		workerCancel()
		return err
	}

	// Cancel workers and wait for drain.
	workerCancel()
	if err := <-workerDone; err != nil {
		slog.Error("worker shutdown error", "error", err)
	}

	// Shutdown tracing exporter.
	if tracingShutdown != nil {
		if err := tracingShutdown(shutdownCtx); err != nil {
			slog.Error("tracing shutdown error", "error", err)
		}
	}

	slog.Info("gandalf stopped")
	return nil
}

// buildProviderClient assembles an *http.Client with the auth transport chain
// for a provider entry. The base transport includes DNS caching and HTTP/2
// (except Ollama which uses HTTP/1.1).
func buildProviderClient(ctx context.Context, p config.ProviderEntry, resolver *dnscache.Resolver) (*http.Client, error) {
	useHTTP2 := p.ResolvedType() != "ollama"
	base := provider.NewTransport(resolver, useHTTP2)

	var transport http.RoundTripper = base

	switch p.ResolvedAuthType() {
	case "gcp_oauth":
		gcpTransport, err := cloudauth.NewGCPOAuthTransport(ctx, base,
			"https://www.googleapis.com/auth/cloud-platform",
		)
		if err != nil {
			return nil, fmt.Errorf("gcp oauth: %w", err)
		}
		transport = gcpTransport
	case "aws_sigv4":
		awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(p.Region))
		if err != nil {
			return nil, fmt.Errorf("aws credentials: %w", err)
		}
		transport = cloudauth.NewAWSSigV4Transport(base, awsCfg.Credentials, p.Region, "bedrock-runtime")
	case "api_key":
		apiKey := p.ResolvedAPIKey()
		if apiKey != "" {
			headerName, prefix := authHeaderForType(p.ResolvedType(), p.ResolvedHosting())
			transport = &cloudauth.APIKeyTransport{
				Key:        apiKey,
				HeaderName: headerName,
				Prefix:     prefix,
				Base:       base,
			}
		}
		// Empty API key: no auth transport (e.g. local Ollama).
	default:
		return nil, fmt.Errorf("unsupported auth type: %q", p.ResolvedAuthType())
	}

	client := &http.Client{Transport: transport}
	if p.TimeoutMs > 0 {
		client.Timeout = time.Duration(p.TimeoutMs) * time.Millisecond
	}
	return client, nil
}

// authHeaderForType returns the (headerName, prefix) for API key auth
// based on provider type and hosting mode.
func authHeaderForType(provType, hosting string) (string, string) {
	switch {
	case provType == "openai" && hosting == "azure":
		return "api-key", ""
	case provType == "openai":
		return "Authorization", "Bearer "
	case provType == "anthropic":
		return "x-api-key", ""
	case provType == "gemini":
		return "x-goog-api-key", ""
	case provType == "ollama":
		return "Authorization", "Bearer "
	default:
		return "Authorization", "Bearer "
	}
}
