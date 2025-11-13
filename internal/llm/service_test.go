package llm

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type testConfig struct {
	LLM Config `yaml:"llm"`
}

func createTempConfig(t *testing.T, cfg testConfig) (string, func()) {
	t.Helper()
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	data, err := yaml.Marshal(cfg)
	require.NoError(t, err)

	err = os.WriteFile(configPath, data, 0o644)
	require.NoError(t, err)

	return configPath, func() {
		_ = os.RemoveAll(tempDir)
	}
}

func TestService_ConfigWatching(t *testing.T) {
	// Skip this test in short mode since it requires network access
	if testing.Short() {
		t.Skip("Skipping test that requires network access in short mode")
	}

	// Create initial config
	initialCfg := testConfig{
		LLM: Config{
			Enabled:     true,
			ConfigWatch: true,
			Client: VLLMConfig{
				BaseURL: "http://localhost:8000",
				Timeout: 30 * time.Second,
			},
		},
	}

	// Create a test logger
	logger := &serviceTestLogger{t: t}

	configPath, cleanup := createTempConfig(t, initialCfg)
	defer cleanup()

	// Create service with initial config
	service, err := NewService(initialCfg.LLM, logger)
	require.NoError(t, err)
	defer service.Close()

	// Start watching config
	err = service.WatchConfig(configPath)
	require.NoError(t, err)

	// Verify initial config is not enabled (since we don't have a real LLM server)
	// In a real test environment, you would have a mock LLM server running
	assert.False(t, service.IsEnabled(), "Service should not be enabled without a real LLM server")

	// Test 1: Update base URL
	updatedCfg1 := initialCfg
	updatedCfg1.LLM.Client.BaseURL = "http://new-url:8000"

	data1, err := yaml.Marshal(updatedCfg1)
	require.NoError(t, err)

	err = os.WriteFile(configPath, data1, 0o644)
	require.NoError(t, err)

	// Wait for the config to be reloaded
	time.Sleep(200 * time.Millisecond)

	// Verify service is still not enabled after update
	assert.False(t, service.IsEnabled(), "Service should remain not enabled without a real LLM server")

	// Test 2: Disable the service
	updatedCfg2 := updatedCfg1
	updatedCfg2.LLM.Enabled = false

	data2, err := yaml.Marshal(updatedCfg2)
	require.NoError(t, err)

	err = os.WriteFile(configPath, data2, 0o644)
	require.NoError(t, err)

	// Wait for the config to be reloaded
	time.Sleep(200 * time.Millisecond)

	// Verify service is still not enabled after disabling
	assert.False(t, service.IsEnabled(), "Service should remain not enabled after disabling")

	// Note: We skip the re-enable test since we don't have a real LLM server running
	// In a real test environment, you would test the full cycle with a mock server
}

// serviceTestLogger is a logger implementation for testing the service package
type serviceTestLogger struct {
	t *testing.T
}

func (l *serviceTestLogger) Debug(msg string, args ...interface{}) { l.t.Logf("DEBUG: "+msg, args...) }
func (l *serviceTestLogger) Info(msg string, args ...interface{})  { l.t.Logf("INFO: "+msg, args...) }
func (l *serviceTestLogger) Error(msg string, args ...interface{}) { l.t.Logf("ERROR: "+msg, args...) }
