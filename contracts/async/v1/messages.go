package asyncv1

import "time"

const SchemaVersion = 1

const (
	SubjectCodeExecute           = "commands.agent.code.execute.v1"
	SubjectLogsExecute           = "commands.agent.logs.execute.v1"
	SubjectDatabaseExecute       = "commands.agent.database.execute.v1"
	SubjectInfrastructureExecute = "commands.agent.infrastructure.execute.v1"
	SubjectAgentResults          = "results.agent.*.completed.v1"
	SubjectAgentResultsPrefix    = "results.agent."
)

type AgentCommand struct {
	SchemaVersion int       `json:"schema_version"`
	MessageID     string    `json:"message_id"`
	TaskID        string    `json:"task_id"`
	SubtaskID     string    `json:"subtask_id"`
	AttemptID     string    `json:"attempt_id"`
	Capability    string    `json:"capability"`
	Objective     string    `json:"objective"`
	Deadline      time.Time `json:"deadline"`
	Traceparent   string    `json:"traceparent,omitempty"`
}

type AgentResult struct {
	SchemaVersion int        `json:"schema_version"`
	MessageID     string     `json:"message_id"`
	TaskID        string     `json:"task_id"`
	SubtaskID     string     `json:"subtask_id"`
	AttemptID     string     `json:"attempt_id"`
	AgentType     string     `json:"agent_type"`
	Success       bool       `json:"success"`
	Summary       string     `json:"summary"`
	Evidence      []Evidence `json:"evidence,omitempty"`
	Warnings      []string   `json:"warnings,omitempty"`
	ErrorCode     string     `json:"error_code,omitempty"`
	Traceparent   string     `json:"traceparent,omitempty"`
	CompletedAt   time.Time  `json:"completed_at"`
}

type Evidence struct {
	Source    string `json:"source"`
	Reference string `json:"reference"`
	Content   string `json:"content"`
}

func ExecuteSubject(capability string) string {
	switch capability {
	case "code":
		return SubjectCodeExecute
	case "logs":
		return SubjectLogsExecute
	case "database":
		return SubjectDatabaseExecute
	case "infrastructure":
		return SubjectInfrastructureExecute
	default:
		return ""
	}
}

func ResultSubject(agentType string) string {
	return SubjectAgentResultsPrefix + agentType + ".completed.v1"
}
