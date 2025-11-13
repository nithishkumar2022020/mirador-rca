package llm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"

	"github.com/miradorstack/mirador-rca/internal/models"
)

// Logger defines the interface for logging
// This allows for different logger implementations to be used with the service
type Logger interface {
	Debug(msg string, args ...interface{})
	Info(msg string, args ...interface{})
	Error(msg string, args ...interface{})
}

// Service provides LLM-powered RCA analysis with LMCache integration
type Service struct {
	client   Client
	clientMu sync.RWMutex
	logger   Logger
	config   atomic.Value // For storing current config
	closeCh  chan struct{}
	wg       sync.WaitGroup
}

// Config holds configuration for the LLM service
type Config struct {
	Enabled     bool
	ConfigWatch bool
	Client      VLLMConfig
}

// NewService creates a new LLM service
// The logger parameter can be nil, in which case no logging will be performed
func NewService(config Config, logger Logger) (*Service, error) {
	s := &Service{
		logger:  logger,
		closeCh: make(chan struct{}),
	}

	s.config.Store(config)

	if !config.Enabled {
		return s, nil
	}

	if err := s.initClient(config); err != nil {
		if logger != nil {
			logger.Error("LLM service initialization failed, proceeding without LLM", "error", err)
		}
		return s, nil
	}

	return s, nil
}

// initClient initializes or updates the LLM client
func (s *Service) initClient(config Config) error {
	if config.Client.BaseURL == "" {
		return fmt.Errorf("LLM client base URL is required when LLM is enabled")
	}

	client := NewVLLMClient(VLLMConfig{
		BaseURL: config.Client.BaseURL,
		Timeout: config.Client.Timeout,
	}, s.logger)

	// Test the connection
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := client.Health(ctx); err != nil {
		return fmt.Errorf("LLM health check failed: %w", err)
	}

	// Update the client atomically
	s.clientMu.Lock()
	defer s.clientMu.Unlock()

	// Close existing client if any
	if s.client != nil {
		s.client.Close()
	}

	s.client = client
	return nil
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
		if s.logger != nil {
			s.logger.Error("LLM analysis failed", "error", err)
		}
		return "", fmt.Errorf("LLM analysis failed: %w", err)
	}

	analysis := strings.TrimSpace(response.Text)
	if s.logger != nil {
		s.logger.Info("LLM RCA analysis completed",
			"incident", incident.IncidentID,
			"latency", response.Latency)
	}

	return analysis, nil
}

// EnhanceCorrelation enhances a correlation with LLM analysis
func (s *Service) EnhanceCorrelation(ctx context.Context, correlation models.CorrelationResult) (string, error) {
	if s.client == nil {
		return "", fmt.Errorf("LLM client not configured")
	}

	// Convert correlation to string for the prompt
	correlationStr := fmt.Sprintf("%+v", correlation)

	prompt := fmt.Sprintf("Enhance the following correlation with additional context and analysis:\n\n%s\n\nEnhancement:", correlationStr)

	options := GenerateOptions{
		MaxTokens:   500,
		Temperature: 0.2,
		TopP:        0.9,
		Stream:      false,
	}

	response, err := s.client.Generate(ctx, prompt, options)
	if err != nil {
		if s.logger != nil {
			s.logger.Error("LLM correlation enhancement failed",
				"error", err.Error())
		}
		return "", fmt.Errorf("LLM correlation enhancement failed: %w", err)
	}

	enhancement := strings.TrimSpace(response.Text)
	if s.logger != nil {
		s.logger.Debug("LLM correlation enhancement completed",
			"latency", response.Latency)
	}

	return enhancement, nil
}

// IsEnabled returns whether LLM analysis is enabled
func (s *Service) IsEnabled() bool {
	return s.client != nil
}

// Close shuts down the service and releases resources
func (s *Service) Close() error {
	close(s.closeCh)
	s.wg.Wait()

	if s.client != nil {
		return s.client.Close()
	}
	return nil
}

// WatchConfig starts watching the config file for changes
func (s *Service) WatchConfig(configPath string) error {
	if !s.IsEnabled() {
		return nil
	}

	abspath, err := filepath.Abs(configPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute config path: %w", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}

	if err := watcher.Add(filepath.Dir(abspath)); err != nil {
		return fmt.Errorf("failed to watch config directory: %w", err)
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer watcher.Close()

		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Name == abspath && (event.Has(fsnotify.Write) || event.Has(fsnotify.Rename)) {
					s.handleConfigChange(abspath)
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				s.logger.Error("config watcher error", "error", err)
			case <-s.closeCh:
				return
			}
		}
	}()

	return nil
}

func (s *Service) handleConfigChange(configPath string) {
	// Add a small delay to handle atomic saves
	time.Sleep(100 * time.Millisecond)

	// Read the config file
	data, err := os.ReadFile(configPath)
	if err != nil {
		s.logger.Error("failed to read config file", "path", configPath, "error", err)
		return
	}

	// Parse the new config
	var newConfig struct {
		LLM Config `yaml:"llm"`
	}
	if err := yaml.Unmarshal(data, &newConfig); err != nil {
		if s.logger != nil {
			s.logger.Error("failed to parse config", "error", err)
		}
		return
	}

	currentConfig := s.config.Load().(Config)
	if newConfig.LLM == currentConfig {
		return // No changes
	}

	if s.logger != nil {
		s.logger.Info("LLM configuration changed, applying updates")
	}
	s.config.Store(newConfig.LLM)

	if newConfig.LLM.Enabled {
		if err := s.initClient(newConfig.LLM); err != nil {
			if s.logger != nil {
				s.logger.Error("failed to update LLM client", "error", err)
			}
			// Keep the old client running
			return
		}
		if s.logger != nil {
			s.logger.Info("LLM client updated successfully")
		}
	} else {
		s.clientMu.Lock()
		if s.client != nil {
			s.client.Close()
			s.client = nil
		}
		s.clientMu.Unlock()
		if s.logger != nil {
			s.logger.Info("LLM client disabled due to config change")
		}
	}
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
