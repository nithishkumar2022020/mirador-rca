package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config captures the minimal settings required to boot the RCA service.
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Clients  ClientsConfig  `yaml:"clients"`
	Weaviate WeaviateConfig `yaml:"weaviate"`
	Logging  LoggingConfig  `yaml:"logging"`
	Rules    RulesConfig    `yaml:"rules"`
	Cache    CacheConfig    `yaml:"cache"`
	LLM      LLMConfig      `yaml:"llm"`
}

// LLMConfig controls local LLM integration (vLLM / Mistral)
type LLMConfig struct {
	Enabled     bool          `yaml:"enabled"`
	BaseURL     string        `yaml:"baseURL"`
	APIKey      string        `yaml:"apiKey"`
	Model       string        `yaml:"model"`
	Timeout     time.Duration `yaml:"timeout"`
	MaxTokens   int           `yaml:"maxTokens"`
	Temperature float64       `yaml:"temperature"`
	// Caching and circuit-breaker settings
	CacheEnabled          bool          `yaml:"cacheEnabled"`
	CacheTTL              time.Duration `yaml:"cacheTTL"`
	CircuitBreakerEnabled bool          `yaml:"circuitBreakerEnabled"`
	CBFailureThreshold    uint32        `yaml:"cbFailureThreshold"`
	CBTimeout             time.Duration `yaml:"cbTimeout"`
}

// ServerConfig controls gRPC listener behaviour.
type ServerConfig struct {
	Address         string        `yaml:"address"`
	MetricsAddress  string        `yaml:"metricsAddress"`
	GracefulTimeout time.Duration `yaml:"gracefulTimeout"`
}

// ClientsConfig groups integrations with Victoria* backends.
type ClientsConfig struct {
	Core CoreClientConfig `yaml:"core"`
}

// CoreClientConfig configures access to mirador-core data aggregation APIs.
type CoreClientConfig struct {
	BaseURL          string        `yaml:"baseURL"`
	MetricsPath      string        `yaml:"metricsPath"`
	LogsPath         string        `yaml:"logsPath"`
	TracesPath       string        `yaml:"tracesPath"`
	ServiceGraphPath string        `yaml:"serviceGraphPath"`
	Timeout          time.Duration `yaml:"timeout"`
}

// WeaviateConfig configures the similarity search cluster.
type WeaviateConfig struct {
	Endpoint string        `yaml:"endpoint"`
	APIKey   string        `yaml:"apiKey"`
	Timeout  time.Duration `yaml:"timeout"`
}

// LoggingConfig controls structured logging.
type LoggingConfig struct {
	Level string `yaml:"level"`
	JSON  bool   `yaml:"json"`
}

// RulesConfig controls rule-pack loading for the recommender.
type RulesConfig struct {
	Path string `yaml:"path"`
}

// CacheConfig controls Valkey-backed caching of expensive lookups.
type CacheConfig struct {
	Enabled             bool          `yaml:"enabled"`
	Addr                string        `yaml:"addr"`
	Username            string        `yaml:"username"`
	Password            string        `yaml:"password"`
	DB                  int           `yaml:"db"`
	DialTimeout         time.Duration `yaml:"dialTimeout"`
	ReadTimeout         time.Duration `yaml:"readTimeout"`
	WriteTimeout        time.Duration `yaml:"writeTimeout"`
	MaxRetries          int           `yaml:"maxRetries"`
	TLS                 bool          `yaml:"tls"`
	SimilarIncidentsTTL time.Duration `yaml:"similarIncidentsTTL"`
	ServiceGraphTTL     time.Duration `yaml:"serviceGraphTTL"`
	PatternsTTL         time.Duration `yaml:"patternsTTL"`
}

// Load initialises Config from a YAML file and optional environment overrides.
func Load(path string) (*Config, error) {
	if path == "" {
		path = os.Getenv("MIRADOR_RCA_CONFIG")
	}

	cfg := defaultConfig()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("config file %s not found: %w", path, err)
			}
			return nil, fmt.Errorf("read config: %w", err)
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parse config: %w", err)
		}
	}

	applyEnvOverrides(&cfg)
	return &cfg, nil
}

func defaultConfig() Config {
	return Config{
		Server: ServerConfig{
			Address:         ":50051",
			MetricsAddress:  ":2112",
			GracefulTimeout: 10 * time.Second,
		},
		Clients: ClientsConfig{
			Core: CoreClientConfig{
				MetricsPath:      "/api/v1/rca/metrics",
				LogsPath:         "/api/v1/rca/logs",
				TracesPath:       "/api/v1/rca/traces",
				ServiceGraphPath: "/api/v1/rca/service-graph",
				Timeout:          5 * time.Second,
			},
		},
		Weaviate: WeaviateConfig{Timeout: 5 * time.Second},
		Logging:  LoggingConfig{Level: "info", JSON: false},
		Rules:    RulesConfig{Path: "configs/rules/default.yaml"},
		Cache: CacheConfig{
			Enabled:             false,
			SimilarIncidentsTTL: 2 * time.Minute,
			ServiceGraphTTL:     5 * time.Minute,
			PatternsTTL:         10 * time.Minute,
			DialTimeout:         2 * time.Second,
			ReadTimeout:         500 * time.Millisecond,
			WriteTimeout:        500 * time.Millisecond,
			MaxRetries:          2,
		},
		LLM: LLMConfig{
			Enabled:               false,
			BaseURL:               "http://vllm.local:8000",
			APIKey:                "",
			Model:                 "mistral-8b",
			Timeout:               4 * time.Second,
			MaxTokens:             512,
			Temperature:           0.0,
			CacheEnabled:          false,
			CacheTTL:              5 * time.Minute,
			CircuitBreakerEnabled: true,
			CBFailureThreshold:    5,
			CBTimeout:             60 * time.Second,
		},
	}
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("MIRADOR_RCA_SERVER_ADDRESS"); v != "" {
		cfg.Server.Address = v
	}
	if v := os.Getenv("MIRADOR_RCA_METRICS_ADDRESS"); v != "" {
		cfg.Server.MetricsAddress = v
	}
	if v := os.Getenv("MIRADOR_CORE_BASE_URL"); v != "" {
		cfg.Clients.Core.BaseURL = v
	}
	if v := os.Getenv("MIRADOR_CORE_METRICS_PATH"); v != "" {
		cfg.Clients.Core.MetricsPath = v
	}
	if v := os.Getenv("MIRADOR_CORE_LOGS_PATH"); v != "" {
		cfg.Clients.Core.LogsPath = v
	}
	if v := os.Getenv("MIRADOR_CORE_TRACES_PATH"); v != "" {
		cfg.Clients.Core.TracesPath = v
	}
	if v := os.Getenv("MIRADOR_CORE_SERVICE_GRAPH_PATH"); v != "" {
		cfg.Clients.Core.ServiceGraphPath = v
	}
	if v := os.Getenv("MIRADOR_RCA_WEAVIATE_URL"); v != "" {
		cfg.Weaviate.Endpoint = v
	}
	if v := os.Getenv("MIRADOR_RCA_WEAVIATE_API_KEY"); v != "" {
		cfg.Weaviate.APIKey = v
	}
	if v := os.Getenv("MIRADOR_RCA_LOG_LEVEL"); v != "" {
		cfg.Logging.Level = v
	}
	if v := os.Getenv("MIRADOR_RCA_LOG_FORMAT"); v == "json" {
		cfg.Logging.JSON = true
	}
	if v := os.Getenv("MIRADOR_RCA_RULES_PATH"); v != "" {
		cfg.Rules.Path = v
	}
	if v := os.Getenv("MIRADOR_RCA_CACHE_ADDR"); v != "" {
		cfg.Cache.Addr = v
	}
	if v := os.Getenv("MIRADOR_RCA_CACHE_ENABLED"); v != "" {
		cfg.Cache.Enabled = strings.EqualFold(v, "true") || strings.EqualFold(v, "1")
	}
	if v := os.Getenv("MIRADOR_RCA_CACHE_USERNAME"); v != "" {
		cfg.Cache.Username = v
	}
	if v := os.Getenv("MIRADOR_RCA_CACHE_PASSWORD"); v != "" {
		cfg.Cache.Password = v
	}
	if v := os.Getenv("MIRADOR_RCA_CACHE_DB"); v != "" {
		if db, err := strconv.Atoi(v); err == nil {
			cfg.Cache.DB = db
		}
	}
	if v := os.Getenv("MIRADOR_RCA_CACHE_TLS"); strings.EqualFold(v, "true") || strings.EqualFold(v, "1") {
		cfg.Cache.TLS = true
	}
	if v := os.Getenv("MIRADOR_RCA_CACHE_DIAL_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Cache.DialTimeout = d
		}
	}
	if v := os.Getenv("MIRADOR_RCA_CACHE_READ_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Cache.ReadTimeout = d
		}
	}
	if v := os.Getenv("MIRADOR_RCA_CACHE_WRITE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Cache.WriteTimeout = d
		}
	}
	if v := os.Getenv("MIRADOR_RCA_CACHE_MAX_RETRIES"); v != "" {
		if retry, err := strconv.Atoi(v); err == nil {
			cfg.Cache.MaxRetries = retry
		}
	}
	if v := os.Getenv("MIRADOR_RCA_CACHE_SIMILAR_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Cache.SimilarIncidentsTTL = d
		}
	}
	if v := os.Getenv("MIRADOR_RCA_CACHE_SERVICE_GRAPH_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Cache.ServiceGraphTTL = d
		}
	}
	if v := os.Getenv("MIRADOR_RCA_CACHE_PATTERNS_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Cache.PatternsTTL = d
		}
	}
	if v := os.Getenv("MIRADOR_RCA_LLM_ENABLED"); v != "" {
		cfg.LLM.Enabled = strings.EqualFold(v, "true") || strings.EqualFold(v, "1")
	}
	if v := os.Getenv("MIRADOR_RCA_LLM_URL"); v != "" {
		cfg.LLM.BaseURL = v
	}
	if v := os.Getenv("MIRADOR_RCA_LLM_MODEL"); v != "" {
		cfg.LLM.Model = v
	}
	if v := os.Getenv("MIRADOR_RCA_LLM_API_KEY"); v != "" {
		cfg.LLM.APIKey = v
	}
	if v := os.Getenv("MIRADOR_RCA_LLM_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.LLM.Timeout = d
		}
	}
	if v := os.Getenv("MIRADOR_RCA_LLM_MAX_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.LLM.MaxTokens = n
		}
	}
	if v := os.Getenv("MIRADOR_RCA_LLM_TEMPERATURE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.LLM.Temperature = f
		}
	}
}
