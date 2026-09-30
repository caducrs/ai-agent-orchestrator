package store

import (
	"encoding/json"
	"time"

	"github.com/caduc/ai-agent-orchestrator/services/orchestrator/internal/domain"
)

type OutboxMessage struct {
	ID      string
	Subject string
	Payload []byte
}

type AgentDescriptor struct {
	ID           string
	Type         string
	Version      string
	Capabilities []string
	Status       string
}

type AggregationInput struct {
	Task domain.Task
}

func marshal(value any) ([]byte, error) {
	return json.Marshal(value)
}

func unmarshalResult(raw []byte) (*domain.AgentResult, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var result domain.AgentResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func unmarshalFinal(raw []byte) (*domain.FinalResult, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var result domain.FinalResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func deadline(now time.Time, seconds int32, fallback time.Duration) time.Time {
	if seconds <= 0 {
		return now.Add(fallback)
	}
	return now.Add(time.Duration(seconds) * time.Second)
}
