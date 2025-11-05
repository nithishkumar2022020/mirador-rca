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
		cfg.Clients.Core.CorrelationPath,
		cfg.Clients.Core.CorrelationEnabled,
		cfg.Clients.Core.Timeout,
		cacheProvider,
		cfg.Cache.ServiceGraphTTL,
		cfg.Cache.SimilarIncidentsTTL, // Use similar incidents TTL for correlations
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

	llmService, err := llm.NewService(llm.Config{
		Enabled: cfg.LLM.Enabled,
		Client: llm.VLLMConfig{
			BaseURL: cfg.LLM.BaseURL,
			Timeout: cfg.LLM.Timeout,
		},
	}, logger)
	if err != nil {
		logger.Error("failed to initialize LLM service", slog.Any("error", err))
		os.Exit(1)
	}
	defer llmService.Close()

	pipeline := engine.NewPipeline(
		logger,
		coreClient,
		weaviateRepo,
		ruleEngine,
		causalityEngine,
		extractors.NewMetricExtractor(),
		extractors.NewLogsExtractor(),
		extractors.NewTracesExtractor(),
		llmService,
	)

	rcaService := services.NewRCAService(logger, coreClient, pipeline, weaviateRepo, llmService)

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

	var restServer *http.Server
	if cfg.Server.RESTAddress != "" {
		mux := http.NewServeMux()
		handler := api.NewRESTHandler(rcaService, logger)
		mux.HandleFunc("/api/v1/investigate", handler.InvestigateIncident)
		mux.HandleFunc("/api/v1/correlations", handler.ListCorrelations)
		mux.HandleFunc("/api/v1/patterns", handler.GetPatterns)
		mux.HandleFunc("/api/v1/feedback", handler.SubmitFeedback)
		mux.HandleFunc("/health", handler.HealthCheck)
		restServer = &http.Server{
			Addr:         cfg.Server.RESTAddress,
			Handler:      mux,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 15 * time.Second,
		}
		go func() {
			logger.Info("REST server listening", slog.String("address", cfg.Server.RESTAddress))
			if err := restServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("REST server exited", slog.Any("error", err))
				stop()
			}
		}()
	}

	<-ctx.Done()
	logger.Info("shutdown signal received")

	if metricsServer != nil {
		metricsCtx, cancelMetrics := context.WithTimeout(context.Background(), 5*time.Second)
		if err := metricsServer.Shutdown(metricsCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Warn("metrics server shutdown", slog.Any("error", err))
		}
		cancelMetrics()
	}

	if restServer != nil {
		restCtx, cancelRest := context.WithTimeout(context.Background(), 5*time.Second)
		if err := restServer.Shutdown(restCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Warn("REST server shutdown", slog.Any("error", err))
		}
		cancelRest()
	}

	// Give remaining goroutines time to finish logging
	time.Sleep(100 * time.Millisecond)
	logger.Info("mirador-rca stopped")
}
