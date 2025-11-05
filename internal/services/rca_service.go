package services

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/miradorstack/mirador-rca/internal/engine"
	"github.com/miradorstack/mirador-rca/internal/llm"
	"github.com/miradorstack/mirador-rca/internal/metrics"
	"github.com/miradorstack/mirador-rca/internal/models"
	"github.com/miradorstack/mirador-rca/internal/repo"
	"github.com/miradorstack/mirador-rca/internal/utils"
)

// CorrelationPatternRepo defines storage operations required for correlation history and patterns.
type CorrelationPatternRepo interface {
	ListCorrelations(ctx context.Context, req models.ListCorrelationsRequest) (models.ListCorrelationsResponse, error)
	FetchPatterns(ctx context.Context, tenantID, service string) ([]models.FailurePattern, error)
	StoreFeedback(ctx context.Context, feedback models.Feedback) error
}

// RCAService implements the REST RCA service.
type RCAService struct {
	logger      *slog.Logger
	coreClient  *repo.MiradorCoreClient
	pipeline    *engine.Pipeline
	historyRepo CorrelationPatternRepo
	llmService  *llm.Service
	latencies   *utils.LatencyTracker
}

// NewRCAService constructs the RCA service facade.
func NewRCAService(logger *slog.Logger, coreClient *repo.MiradorCoreClient, pipeline *engine.Pipeline, historyRepo CorrelationPatternRepo, llmService *llm.Service) *RCAService {
	if logger == nil {
		logger = slog.Default()
	}
	return &RCAService{
		logger:      logger,
		coreClient:  coreClient,
		pipeline:    pipeline,
		historyRepo: historyRepo,
		llmService:  llmService,
		latencies:   utils.NewLatencyTracker(1024),
	}
}

// InvestigateIncidentREST performs RCA investigation via REST API
func (s *RCAService) InvestigateIncidentREST(ctx context.Context, req models.InvestigationRequest) (models.CorrelationResult, error) {
	if req.IncidentID == "" {
		return models.CorrelationResult{}, fmt.Errorf("incident_id is required")
	}
	if s.pipeline == nil {
		return models.CorrelationResult{}, fmt.Errorf("pipeline not configured")
	}

	start := time.Now()
	defer func() {
		s.latencies.Observe(time.Since(start))
	}()

	result, err := s.pipeline.Investigate(ctx, req)
	if err != nil {
		s.logger.Error("Pipeline execution failed", "incident", req.IncidentID, "error", err)
		metrics.ObserveInvestigation(time.Since(start), metrics.OutcomeError)
		return models.CorrelationResult{}, fmt.Errorf("investigation failed: %w", err)
	}

	metrics.ObserveInvestigation(time.Since(start), metrics.OutcomeSuccess)
	s.logger.Info("Investigation completed", "incident", req.IncidentID, "correlation", result.CorrelationID, "confidence", result.Confidence)

	return result, nil
}

// ListCorrelationsREST retrieves correlation history via REST API
func (s *RCAService) ListCorrelationsREST(ctx context.Context, req models.ListCorrelationsRequest) (models.ListCorrelationsResponse, error) {
	if s.historyRepo == nil {
		return models.ListCorrelationsResponse{}, fmt.Errorf("history repository not configured")
	}

	start := time.Now()
	defer func() {
		s.latencies.Observe(time.Since(start))
	}()

	response, err := s.historyRepo.ListCorrelations(ctx, req)
	if err != nil {
		s.logger.Error("Failed to list correlations", "tenant", req.TenantID, "error", err)
		return models.ListCorrelationsResponse{}, fmt.Errorf("failed to list correlations: %w", err)
	}

	return response, nil
}

// GetPatternsREST retrieves failure patterns via REST API
func (s *RCAService) GetPatternsREST(ctx context.Context, tenantID, service string) ([]models.FailurePattern, error) {
	if s.historyRepo == nil {
		return nil, fmt.Errorf("history repository not configured")
	}

	start := time.Now()
	defer func() {
		s.latencies.Observe(time.Since(start))
	}()

	patterns, err := s.historyRepo.FetchPatterns(ctx, tenantID, service)
	if err != nil {
		s.logger.Error("Failed to fetch patterns", "tenant", tenantID, "service", service, "error", err)
		return nil, fmt.Errorf("failed to fetch patterns: %w", err)
	}

	return patterns, nil
}

// SubmitFeedbackREST submits user feedback via REST API
func (s *RCAService) SubmitFeedbackREST(ctx context.Context, feedback models.Feedback) error {
	if feedback.CorrelationID == "" {
		return fmt.Errorf("correlation_id is required")
	}
	if s.historyRepo == nil {
		return fmt.Errorf("history repository not configured")
	}

	start := time.Now()
	defer func() {
		s.latencies.Observe(time.Since(start))
	}()

	if err := s.historyRepo.StoreFeedback(ctx, feedback); err != nil {
		s.logger.Error("Failed to store feedback", "correlation", feedback.CorrelationID, "error", err)
		return fmt.Errorf("failed to store feedback: %w", err)
	}

	s.logger.Info("Feedback submitted", "correlation", feedback.CorrelationID, "correct", feedback.Correct)
	return nil
}

// HealthCheckREST performs health check via REST API
func (s *RCAService) HealthCheckREST(ctx context.Context) string {
	// Basic health check - could be extended to check dependencies
	return "healthy"
}
