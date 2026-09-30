package config

import (
	"fmt"
	"os"
	"time"
)

type Config struct {
	GRPCAddr       string
	DatabaseURL    string
	Provider       string
	BaseURL        string
	APIKey         string
	Model          string
	ServiceVersion string
	RequestTimeout time.Duration
	StartupTimeout time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		GRPCAddr:       value("GRPC_ADDR", ":9091"),
		DatabaseURL:    value("DATABASE_URL", "postgres://llm:llm@localhost:5432/llm_gateway?sslmode=disable"),
		Provider:       value("LLM_PROVIDER", "local"),
		BaseURL:        value("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		APIKey:         os.Getenv("OPENAI_API_KEY"),
		Model:          value("OPENAI_MODEL", "gpt-4.1-mini"),
		ServiceVersion: value("SERVICE_VERSION", "dev"),
		RequestTimeout: duration("LLM_REQUEST_TIMEOUT", 45*time.Second),
		StartupTimeout: duration("STARTUP_TIMEOUT", 30*time.Second),
	}
	if cfg.Provider != "local" && cfg.Provider != "openai" {
		return Config{}, fmt.Errorf("LLM_PROVIDER must be local or openai")
	}
	if cfg.Provider == "openai" && cfg.APIKey == "" {
		return Config{}, fmt.Errorf("OPENAI_API_KEY is required for openai provider")
	}
	if cfg.RequestTimeout <= 0 || cfg.StartupTimeout <= 0 {
		return Config{}, fmt.Errorf("timeouts must be positive")
	}
	return cfg, nil
}

func value(name, fallback string) string {
	if current := os.Getenv(name); current != "" {
		return current
	}
	return fallback
}

func duration(name string, fallback time.Duration) time.Duration {
	current := os.Getenv(name)
	if current == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(current)
	if err != nil {
		return -1
	}
	return parsed
}
