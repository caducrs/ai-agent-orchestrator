package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
)

type Config struct {
	GRPCAddr            string
	DatabaseURL         string
	NATSURL             string
	LLMGRPCAddr         string
	ServiceVersion      string
	TaskTimeout         time.Duration
	SubtaskTimeout      time.Duration
	MaxSubtasks         int
	SchedulerInterval   time.Duration
	OutboxInterval      time.Duration
	ResultConcurrency   int
	LocalDevelopment    bool
	StartupTimeout      time.Duration
	ShutdownGracePeriod time.Duration
	InstanceID          string
	ReconcileInterval   time.Duration
	PlanningLease       time.Duration
	AggregationLease    time.Duration
	AttemptResultGrace  time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		GRPCAddr:            env("GRPC_ADDR", ":9090"),
		DatabaseURL:         env("DATABASE_URL", "postgres://orchestrator:orchestrator@localhost:5432/orchestrator?sslmode=disable"),
		NATSURL:             env("NATS_URL", "nats://localhost:4222"),
		LLMGRPCAddr:         env("LLM_GRPC_ADDR", "localhost:9091"),
		ServiceVersion:      env("SERVICE_VERSION", "dev"),
		TaskTimeout:         duration("TASK_TIMEOUT", 10*time.Minute),
		SubtaskTimeout:      duration("SUBTASK_TIMEOUT", 2*time.Minute),
		MaxSubtasks:         integer("MAX_SUBTASKS", 32),
		SchedulerInterval:   duration("SCHEDULER_INTERVAL", 500*time.Millisecond),
		OutboxInterval:      duration("OUTBOX_INTERVAL", 250*time.Millisecond),
		ResultConcurrency:   integer("RESULT_CONCURRENCY", 8),
		LocalDevelopment:    boolean("LOCAL_DEVELOPMENT_MODE", false),
		StartupTimeout:      duration("STARTUP_TIMEOUT", 30*time.Second),
		ShutdownGracePeriod: duration("SHUTDOWN_GRACE_PERIOD", 30*time.Second),
		InstanceID:          env("INSTANCE_ID", defaultInstanceID()),
		ReconcileInterval:   duration("RECONCILE_INTERVAL", 5*time.Second),
		PlanningLease:       duration("PLANNING_LEASE", 2*time.Minute),
		AggregationLease:    duration("AGGREGATION_LEASE", 2*time.Minute),
		AttemptResultGrace:  duration("ATTEMPT_RESULT_GRACE", 30*time.Second),
	}
	if cfg.MaxSubtasks < 1 || cfg.MaxSubtasks > 256 {
		return Config{}, fmt.Errorf("MAX_SUBTASKS must be between 1 and 256")
	}
	if cfg.ResultConcurrency < 1 || cfg.ResultConcurrency > 256 {
		return Config{}, fmt.Errorf("RESULT_CONCURRENCY must be between 1 and 256")
	}
	for name, value := range map[string]time.Duration{
		"TASK_TIMEOUT": cfg.TaskTimeout, "SUBTASK_TIMEOUT": cfg.SubtaskTimeout,
		"SCHEDULER_INTERVAL": cfg.SchedulerInterval, "OUTBOX_INTERVAL": cfg.OutboxInterval,
		"STARTUP_TIMEOUT": cfg.StartupTimeout, "SHUTDOWN_GRACE_PERIOD": cfg.ShutdownGracePeriod,
		"RECONCILE_INTERVAL": cfg.ReconcileInterval, "PLANNING_LEASE": cfg.PlanningLease,
		"AGGREGATION_LEASE": cfg.AggregationLease, "ATTEMPT_RESULT_GRACE": cfg.AttemptResultGrace,
	} {
		if value <= 0 {
			return Config{}, fmt.Errorf("%s must be positive", name)
		}
	}
	return cfg, nil
}

// defaultInstanceID identifies this replica as lease owner; the random suffix
// keeps restarted containers with a reused hostname from inheriting leases.
func defaultInstanceID() string {
	host, err := os.Hostname()
	if err != nil {
		host = "orchestrator"
	}
	return host + "-" + uuid.NewString()
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func duration(name string, fallback time.Duration) time.Duration {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return -1
	}
	return parsed
}

func integer(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return -1
	}
	return parsed
}

func boolean(name string, fallback bool) bool {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}
