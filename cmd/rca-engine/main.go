package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/miradorstack/mirador-rca/internal/api"
	"github.com/miradorstack/mirador-rca/internal/cache"
	"github.com/miradorstack/mirador-rca/internal/config"
	"github.com/miradorstack/mirador-rca/internal/engine"
	"github.com/miradorstack/mirador-rca/internal/extractors"
	"github.com/miradorstack/mirador-rca/internal/llm"
	"github.com/miradorstack/mirador-rca/internal/metrics"
	"github.com/miradorstack/mirador-rca/internal/repo"
	"github.com/miradorstack/mirador-rca/internal/services"
	"github.com/miradorstack/mirador-rca/internal/utils"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "", "Path to configuration file")
	flag.Parse()

	cfg, err := config.Load(configPath)
	if err != nil {
		slog.Error("failed to load config", slog.String("path", configPath), slog.Any("error", err))
		os.Exit(1)
	}
	// Store runtime config in the centralized runtime store and start watcher.
	config.SetRuntimeConfig(cfg)

	logger := utils.NewLogger(cfg.Logging.Level, cfg.Logging.JSON)
	logger.Info("starting mirador-rca", slog.String("address", cfg.Server.Address))

	if err := metrics.Register(prometheus.DefaultRegisterer); err != nil {
		logger.Error("failed to register metrics", slog.Any("error", err))
		os.Exit(1)
	}

	var cacheProvider cache.Provider = cache.NoopProvider{}
	var valkeyCloser cache.Provider
	if cfg.Cache.Enabled && cfg.Cache.Addr != "" {
		provider, err := cache.NewValkeyProvider(cache.ValkeyConfig{
			Addr:         cfg.Cache.Addr,
			Username:     cfg.Cache.Username,
			Password:     cfg.Cache.Password,
			DB:           cfg.Cache.DB,
			DialTimeout:  cfg.Cache.DialTimeout,
			ReadTimeout:  cfg.Cache.ReadTimeout,
			WriteTimeout: cfg.Cache.WriteTimeout,
			MaxRetries:   cfg.Cache.MaxRetries,
			TLS:          cfg.Cache.TLS,
		})
		if err != nil {
			logger.Warn("valkey cache unavailable", slog.Any("error", err))
		} else {
			cacheProvider = provider
			valkeyCloser = provider
		}
	}
	if valkeyCloser != nil {
		defer valkeyCloser.Close()
	}

	coreClient := repo.NewMiradorCoreClient(
		cfg.Clients.Core.BaseURL,
		cfg.Clients.Core.MetricsPath,
		cfg.Clients.Core.LogsPath,
		cfg.Clients.Core.TracesPath,
		cfg.Clients.Core.ServiceGraphPath,
		cfg.Clients.Core.Timeout,
		cacheProvider,
		cfg.Cache.ServiceGraphTTL,
	)

	weaviateRepo := repo.NewWeaviateRepo(
		cfg.Weaviate.Endpoint,
		cfg.Weaviate.APIKey,
		cfg.Weaviate.Timeout,
		cacheProvider,
		cfg.Cache.SimilarIncidentsTTL,
		cfg.Cache.PatternsTTL,
	)

	ruleEngine, err := engine.NewRuleEngine(cfg.Rules.Path, logger)
	if err != nil {
		logger.Error("failed to load rule pack", slog.Any("error", err))
		os.Exit(1)
	}
	causalityEngine := engine.NewCausalityEngine(logger)

	pipeline := engine.NewPipeline(
		logger,
		coreClient,
		weaviateRepo,
		ruleEngine,
		causalityEngine,
		extractors.NewMetricExtractor(),
		extractors.NewLogsExtractor(),
		extractors.NewTracesExtractor(),
	)

	// Initialize LLM client (optional) and attach to pipeline. We create the client
	// even if disabled so operators can flip the runtime feature flag without restart.
	// The client implementation uses resty and is safe in air-gapped setups when
	// configured with local endpoints.
	if cfg.LLM.BaseURL != "" {
		llmClient := llm.NewClient(cfg.LLM, logger)
		pipeline.SetLLMClient(llmClient)
	}

	rcaService := services.NewRCAService(logger, coreClient, pipeline, weaviateRepo)

	server, err := api.NewServer(cfg.Server, rcaService)
	if err != nil {
		logger.Error("failed to create gRPC server", slog.Any("error", err))
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if configPath != "" {
		if err := config.WatchConfig(ctx, configPath, logger); err != nil {
			logger.Warn("failed to start config watcher", slog.Any("error", err))
		}
	}

	var metricsServer *http.Server
	if cfg.Server.MetricsAddress != "" {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		metricsServer = &http.Server{
			Addr:         cfg.Server.MetricsAddress,
			Handler:      mux,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 15 * time.Second,
		}
		go func() {
			logger.Info("metrics server listening", slog.String("address", cfg.Server.MetricsAddress))
			if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("metrics server exited", slog.Any("error", err))
				stop()
			}
		}()
	}

	go func() {
		if serveErr := server.Start(); serveErr != nil {
			logger.Error("gRPC server exited", slog.Any("error", serveErr))
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.GracefulTimeout)
	defer cancel()
	server.Shutdown(shutdownCtx)

	if metricsServer != nil {
		metricsCtx, cancelMetrics := context.WithTimeout(context.Background(), 5*time.Second)
		if err := metricsServer.Shutdown(metricsCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Warn("metrics server shutdown", slog.Any("error", err))
		}
		cancelMetrics()
	}

	// Give remaining goroutines time to finish logging
	time.Sleep(100 * time.Millisecond)
	logger.Info("mirador-rca stopped")
}
