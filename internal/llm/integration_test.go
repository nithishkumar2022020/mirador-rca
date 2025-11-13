//go:build test

package llm

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testLogger is a simple logger for testing
type testLogger struct {
	t *testing.T
}

func (l *testLogger) Debug(msg string, args ...interface{}) { l.t.Logf("DEBUG: "+msg, args...) }
func (l *testLogger) Info(msg string, args ...interface{})  { l.t.Logf("INFO: "+msg, args...) }
func (l *testLogger) Error(msg string, args ...interface{}) { l.t.Logf("ERROR: "+msg, args...) }

const testConfigTemplate = `llm:
  enabled: %v
  watch: %v
  baseURL: "%s"
  timeout: %s`

func TestLLMService_Integration(t *testing.T) {
	// Skip in short mode
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Start mock server
	mockSrv := newMockServer()
	defer mockSrv.Close()

	t.Logf("Mock server started at %s", mockSrv.URL())

	// Create test config file with mock server URL
	configPath := "test-config.yaml"
	configContent := fmt.Sprintf(testConfigTemplate, true, true, mockSrv.URL(), "30s")
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err, "Failed to create test config file")
	defer os.Remove(configPath)

	// Create service
	t.Log("Creating LLM service...")
	service, err := NewService(Config{
		Enabled:     true,
		ConfigWatch: true,
		Client: VLLMConfig{
			BaseURL: mockSrv.URL(),
			Timeout: 30 * time.Second,
		},
	}, nil) // Using nil logger for simplicity in test
	require.NoError(t, err, "Failed to create LLM service")
	defer service.Close()

	// Start watching config
	t.Log("Starting config watcher...")
	err = service.WatchConfig(configPath)
	require.NoError(t, err, "Failed to start config watcher")

	// Test 1: Verify service is enabled
	t.Run("Service should be enabled", func(t *testing.T) {
		require.True(t, service.IsEnabled(), "Service should be enabled")
	})

	// Test 2: Test health check
	t.Run("Health check should pass", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Test with valid URL
		err := service.client.Health(ctx)
		require.NoError(t, err, "Health check should pass with valid URL")

		// Note: We can't directly modify the client's baseURL as it's not exposed
		// This is a limitation of the current design. In a real test, we might want to:
		// 1. Add a test helper to modify the client's baseURL
		// 2. Or test this at a higher level by modifying the config file
	})

	// Test 3: Test config reload
	t.Run("Config reload should work", func(t *testing.T) {
		updatedConfig := fmt.Sprintf(testConfigTemplate, true, true, mockSrv.URL(), "60s")

		err := os.WriteFile(configPath, []byte(updatedConfig), 0644)
		require.NoError(t, err, "Failed to update config file")

		// Wait for config reload
		time.Sleep(500 * time.Millisecond)

		// Verify service is still enabled after config reload
		require.True(t, service.IsEnabled(), "Service should still be enabled after config update")
	})

	// Test 4: Test completions
	t.Run("Completions should work", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Test with a simple prompt
		prompt := "Test prompt"
		resp, err := service.client.Generate(ctx, prompt, GenerateOptions{})
		require.NoError(t, err, "Generate should not return error")
		require.NotNil(t, resp, "Response should not be nil")
		require.Contains(t, resp.Text, prompt, "Response text should contain the prompt")
	})
}
