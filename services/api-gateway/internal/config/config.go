package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddr             string
	OrchestratorGRPCAddr string
	RedisAddr            string
	RedisPassword        string
	LocalDevelopment     bool
	DevelopmentToken     string
	DevelopmentSubject   string
	ServiceVersion       string
	RequestTimeout       time.Duration
	ShutdownTimeout      time.Duration
	SSEHeartbeat         time.Duration
	SSEBuffer            int
	RatePerSecond        int64
	RateBurst            int64
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:             value("HTTP_ADDR", ":8080"),
		OrchestratorGRPCAddr: value("ORCHESTRATOR_GRPC_ADDR", "localhost:9090"),
		RedisAddr:            value("REDIS_ADDR", "localhost:6379"),
		RedisPassword:        os.Getenv("REDIS_PASSWORD"),
		LocalDevelopment:     boolean("LOCAL_DEVELOPMENT_MODE", false),
		DevelopmentToken:     value("DEVELOPMENT_TOKEN", "dev-token"),
		DevelopmentSubject:   value("DEVELOPMENT_SUBJECT", "portfolio-user"),
		ServiceVersion:       value("SERVICE_VERSION", "dev"),
		RequestTimeout:       duration("REQUEST_TIMEOUT", 10*time.Second),
		ShutdownTimeout:      duration("SHUTDOWN_TIMEOUT", 30*time.Second),
		SSEHeartbeat:         duration("SSE_HEARTBEAT", 15*time.Second),
		SSEBuffer:            integer("SSE_BUFFER", 128),
		RatePerSecond:        int64(integer("RATE_PER_SECOND", 20)),
		RateBurst:            int64(integer("RATE_BURST", 40)),
	}
	if !cfg.LocalDevelopment {
		return Config{}, fmt.Errorf("only LOCAL_DEVELOPMENT_MODE is implemented; configure an OIDC adapter before production")
	}
	if cfg.DevelopmentToken == "" || cfg.RequestTimeout <= 0 || cfg.ShutdownTimeout <= 0 || cfg.SSEHeartbeat <= 0 || cfg.SSEBuffer <= 0 || cfg.RatePerSecond <= 0 || cfg.RateBurst <= 0 {
		return Config{}, fmt.Errorf("invalid API Gateway configuration")
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

func integer(name string, fallback int) int {
	current := os.Getenv(name)
	if current == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(current)
	if err != nil {
		return -1
	}
	return parsed
}

func boolean(name string, fallback bool) bool {
	current := os.Getenv(name)
	if current == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(current)
	if err != nil {
		return fallback
	}
	return parsed
}
