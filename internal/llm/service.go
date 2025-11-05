package llm

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/miradorstack/mirador-rca/internal/models"
)

// Service provides LLM-powered RCA analysis with LMCache integration
type Service struct {
	client Client
	logger *slog.Logger
	cache *LLMCache
}

// Config holds configuration for the LLM service
type Config struct {
	Enabled bool
	Client  VLLMConfig
}

// NewService creates a new LLM service
func NewService(config Config, logger *slog.Logger, registerer prometheus.Registerer) (*Service, error) {
	if !config.Enabled {
		return &Service{logger: logger}, nil
	}


	if config.Client.BaseURL == "" {
		return nil, fmt.Errorf("LLM client base URL is required when LLM is enabled")
	}

	client := NewVLLMClient(config.Client, logger, registerer)
	cache := NewCache(1*time.Hour, registerer)
	// Test the connection
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := client.Health(ctx); err != nil {
		logger.Warn("LLM service health check failed, proceeding without LLM", slog.Any("error", err))
		return &Service{logger: logger}, nil
	}

	return &Service{
		client: client,
		logger: logger,
		cache: cache,
	}, nil
}



// AnalyzeIncident performs LLM-powered RCA analysis
func (s *Service) AnalyzeIncident(ctx context.Context, incident models.InvestigationRequest, signals models.InvestigationRequest) (string, error) {
	if s.client == nil {
		return "", fmt.Errorf("LLM client not configured")
	}

	prompt := s.buildRCAPrompt(incident, signals)

	options := GenerateOptions{
		MaxTokens:   1000,
		Temperature: 0.1, // Low temperature for consistent analysis
		TopP:        0.9,
		Stream:      false,
	}

	response, err := s.client.Generate(ctx, prompt, options)
	if err != nil {
		return "", fmt.Errorf("LLM analysis failed: %w", err)
	}

	analysis := strings.TrimSpace(response.Text)
	s.logger.Info("LLM RCA analysis completed",
		slog.String("incident", incident.IncidentID),
		slog.Duration("latency", response.Latency),
		slog.Int("tokens_used", response.Usage.TotalTokens))

	return analysis, nil
}

// EnhanceCorrelation provides LLM-enhanced correlation analysis
func (s *Service) EnhanceCorrelation(ctx context.Context, correlation models.CorrelationResult) (string, error) {
	if s.client == nil {
		return "", fmt.Errorf("LLM client not configured")
	}

	prompt := s.buildCorrelationPrompt(correlation)

	options := GenerateOptions{
		MaxTokens:   500,
		Temperature: 0.1,
		TopP:        0.9,
		Stream:      false,
	}

	response, err := s.client.Generate(ctx, prompt, options)
	if err != nil {
		return "", fmt.Errorf("LLM correlation enhancement failed: %w", err)
	}

	enhancement := strings.TrimSpace(response.Text)
	s.logger.Debug("LLM correlation enhancement completed",
		slog.String("correlation", correlation.CorrelationID),
		slog.Duration("latency", response.Latency))

	return enhancement, nil
}

// IsEnabled returns whether LLM analysis is enabled
func (s *Service) IsEnabled() bool {
	return s.client != nil
}

// Close closes the LLM service
func (s *Service) Close() error {
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}

// buildRCAPrompt constructs a prompt for RCA analysis
func (s *Service) buildRCAPrompt(incident models.InvestigationRequest, signals models.InvestigationRequest) string {
	var sb strings.Builder

	sb.WriteString("You are an expert SRE conducting root cause analysis for an incident.\n\n")
	sb.WriteString("INCIDENT DETAILS:\n")
	sb.WriteString(fmt.Sprintf("- Incident ID: %s\n", incident.IncidentID))
	sb.WriteString(fmt.Sprintf("- Symptoms: %s\n", strings.Join(incident.Symptoms, ", ")))
	sb.WriteString(fmt.Sprintf("- Affected Services: %s\n", strings.Join(incident.AffectedServices, ", ")))
	sb.WriteString(fmt.Sprintf("- Time Range: %s to %s\n", incident.TimeRange.Start.Format(time.RFC3339), incident.TimeRange.End.Format(time.RFC3339)))

	sb.WriteString("\nAVAILABLE DATA:\n")
	if len(signals.Symptoms) > 0 {
		sb.WriteString(fmt.Sprintf("- Additional Context: %s\n", strings.Join(signals.Symptoms, ", ")))
	}

	sb.WriteString("\nTASK:\n")
	sb.WriteString("Provide a concise root cause analysis with:\n")
	sb.WriteString("1. Most likely root cause\n")
	sb.WriteString("2. Confidence level (High/Medium/Low)\n")
	sb.WriteString("3. Key evidence from the symptoms\n")
	sb.WriteString("4. Recommended immediate actions\n")
	sb.WriteString("5. Prevention measures\n\n")
	sb.WriteString("Keep your analysis focused and actionable. Use LMCache for efficient inference.\n")

	return sb.String()
}

// buildCorrelationPrompt constructs a prompt for correlation enhancement
func (s *Service) buildCorrelationPrompt(correlation models.CorrelationResult) string {
	var sb strings.Builder

	sb.WriteString("You are analyzing a correlation result from an incident investigation.\n\n")
	sb.WriteString("CORRELATION DETAILS:\n")
	sb.WriteString(fmt.Sprintf("- Correlation ID: %s\n", correlation.CorrelationID))
	sb.WriteString(fmt.Sprintf("- Root Cause: %s\n", correlation.RootCause))
	sb.WriteString(fmt.Sprintf("- Confidence: %.2f\n", correlation.Confidence))
	sb.WriteString(fmt.Sprintf("- Affected Services: %s\n", strings.Join(correlation.AffectedServices, ", ")))

	if len(correlation.Recommendations) > 0 {
		sb.WriteString(fmt.Sprintf("- Current Recommendations: %s\n", strings.Join(correlation.Recommendations, "; ")))
	}

	sb.WriteString("\nTASK:\n")
	sb.WriteString("Provide additional insights to enhance this correlation analysis:\n")
	sb.WriteString("1. Additional potential contributing factors\n")
	sb.WriteString("2. Similar incident patterns to watch for\n")
	sb.WriteString("3. Metrics or logs that would confirm this root cause\n")
	sb.WriteString("4. Alternative hypotheses to consider\n\n")
	sb.WriteString("Be concise and focus on actionable intelligence.\n")

	return sb.String()
}
