package domain

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

type TaskStatus string

const (
	TaskQueued             TaskStatus = "QUEUED"
	TaskPlanning           TaskStatus = "PLANNING"
	TaskRunning            TaskStatus = "RUNNING"
	TaskAggregating        TaskStatus = "AGGREGATING"
	TaskCompleted          TaskStatus = "COMPLETED"
	TaskPartiallyCompleted TaskStatus = "PARTIALLY_COMPLETED"
	TaskFailed             TaskStatus = "FAILED"
	TaskCancelled          TaskStatus = "CANCELLED"
)

type SubtaskStatus string

const (
	SubtaskPending   SubtaskStatus = "PENDING"
	SubtaskBlocked   SubtaskStatus = "BLOCKED"
	SubtaskReady     SubtaskStatus = "READY"
	SubtaskRunning   SubtaskStatus = "RUNNING"
	SubtaskSucceeded SubtaskStatus = "SUCCEEDED"
	SubtaskFailed    SubtaskStatus = "FAILED"
	SubtaskSkipped   SubtaskStatus = "SKIPPED"
	SubtaskCancelled SubtaskStatus = "CANCELLED"
)

var ErrInvalidTransition = errors.New("invalid state transition")

var taskTransitions = map[TaskStatus]map[TaskStatus]bool{
	TaskQueued:      {TaskPlanning: true, TaskCancelled: true},
	TaskPlanning:    {TaskRunning: true, TaskFailed: true, TaskCancelled: true},
	TaskRunning:     {TaskAggregating: true, TaskFailed: true, TaskCancelled: true},
	TaskAggregating: {TaskCompleted: true, TaskPartiallyCompleted: true, TaskFailed: true, TaskCancelled: true},
}

func ValidateTaskTransition(from, to TaskStatus) error {
	if from == to && IsTaskTerminal(from) {
		return nil
	}
	if taskTransitions[from][to] {
		return nil
	}
	return fmt.Errorf("%w: task %s -> %s", ErrInvalidTransition, from, to)
}

func IsTaskTerminal(status TaskStatus) bool {
	return status == TaskCompleted || status == TaskPartiallyCompleted || status == TaskFailed || status == TaskCancelled
}

func IsSubtaskTerminal(status SubtaskStatus) bool {
	return status == SubtaskSucceeded || status == SubtaskFailed || status == SubtaskSkipped || status == SubtaskCancelled
}

type Task struct {
	ID           string       `json:"id"`
	OwnerSubject string       `json:"owner_subject"`
	Description  string       `json:"description"`
	Status       TaskStatus   `json:"status"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
	DeadlineAt   time.Time    `json:"deadline_at"`
	Subtasks     []Subtask    `json:"subtasks,omitempty"`
	FinalResult  *FinalResult `json:"final_result,omitempty"`
	ErrorCode    string       `json:"error_code,omitempty"`
}

type PlannedSubtask struct {
	ID             string
	Description    string
	Capability     string
	DependsOn      []string
	TimeoutSeconds int32
	MaxAttempts    int32
}

type Subtask struct {
	ID               string        `json:"id"`
	TaskID           string        `json:"task_id"`
	Description      string        `json:"description"`
	Capability       string        `json:"capability"`
	Status           SubtaskStatus `json:"status"`
	DependsOn        []string      `json:"depends_on,omitempty"`
	Attempt          int32         `json:"attempt"`
	MaxAttempts      int32         `json:"max_attempts"`
	TimeoutSeconds   int32         `json:"timeout_seconds"`
	CurrentAttemptID string        `json:"current_attempt_id,omitempty"`
	Result           *AgentResult  `json:"result,omitempty"`
	ErrorCode        string        `json:"error_code,omitempty"`
}

type AgentResult struct {
	Summary   string     `json:"summary"`
	Evidence  []Evidence `json:"evidence,omitempty"`
	Warnings  []string   `json:"warnings,omitempty"`
	Truncated bool       `json:"truncated"`
}

type Evidence struct {
	Source    string `json:"source"`
	Reference string `json:"reference"`
	Content   string `json:"content"`
}

type FinalResult struct {
	Summary     string     `json:"summary"`
	Conclusions []string   `json:"conclusions,omitempty"`
	Evidence    []Evidence `json:"evidence,omitempty"`
	Failures    []string   `json:"failures,omitempty"`
	Limitations []string   `json:"limitations,omitempty"`
}

type Event struct {
	TaskID      string    `json:"task_id"`
	EventID     int64     `json:"event_id"`
	Type        string    `json:"type"`
	OccurredAt  time.Time `json:"occurred_at"`
	PayloadJSON string    `json:"payload_json"`
}

func ValidatePlan(plan []PlannedSubtask, max int, capabilities map[string]bool) error {
	if len(plan) == 0 || len(plan) > max {
		return fmt.Errorf("plan must contain between 1 and %d subtasks", max)
	}
	byID := make(map[string]PlannedSubtask, len(plan))
	for _, item := range plan {
		if item.ID == "" || item.Description == "" || !capabilities[item.Capability] {
			return fmt.Errorf("invalid subtask %q", item.ID)
		}
		if _, exists := byID[item.ID]; exists {
			return fmt.Errorf("duplicate subtask id %q", item.ID)
		}
		byID[item.ID] = item
	}
	indegree := make(map[string]int, len(plan))
	children := make(map[string][]string, len(plan))
	for _, item := range plan {
		seen := map[string]bool{}
		for _, dependency := range item.DependsOn {
			if dependency == item.ID || seen[dependency] {
				return fmt.Errorf("invalid dependency %q for %q", dependency, item.ID)
			}
			if _, exists := byID[dependency]; !exists {
				return fmt.Errorf("unknown dependency %q", dependency)
			}
			seen[dependency] = true
			indegree[item.ID]++
			children[dependency] = append(children[dependency], item.ID)
		}
	}
	queue := make([]string, 0, len(plan))
	for id := range byID {
		if indegree[id] == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)
	visited := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		visited++
		for _, child := range children[id] {
			indegree[child]--
			if indegree[child] == 0 {
				queue = append(queue, child)
				sort.Strings(queue)
			}
		}
	}
	if visited != len(plan) {
		return errors.New("plan contains a dependency cycle")
	}
	return nil
}

func DeterministicFallback(description string, subtasks []Subtask) FinalResult {
	ordered := append([]Subtask(nil), subtasks...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	result := FinalResult{Summary: "Análise consolidada para: " + description}
	for _, subtask := range ordered {
		if subtask.Status == SubtaskSucceeded && subtask.Result != nil {
			result.Conclusions = append(result.Conclusions, subtask.Result.Summary)
			result.Evidence = append(result.Evidence, subtask.Result.Evidence...)
		} else {
			result.Failures = append(result.Failures, fmt.Sprintf("%s: %s", subtask.Capability, subtask.ErrorCode))
		}
	}
	if len(result.Evidence) == 0 {
		result.Limitations = append(result.Limitations, "Nenhuma evidência foi coletada")
	}
	return result
}
