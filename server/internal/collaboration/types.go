package collaboration

import "encoding/json"

const (
	RoleOrchestrator = "orchestrator"
	RoleWorker       = "worker"

	EventBriefCreated               = "brief_created"
	EventMemorySnapshotAdded        = "memory_snapshot_added"
	EventAssignmentCreated          = "assignment_created"
	EventWorkerHandoffAdded         = "worker_handoff_added"
	EventOrchestratorSynthesisAdded = "orchestrator_synthesis_added"
	EventQuestionRaised             = "question_raised"
	EventDecisionRecorded           = "decision_recorded"
)

type TaskContext struct {
	Role                 string `json:"role,omitempty"`
	WorkroomID           string `json:"workroom_id,omitempty"`
	TicketMemoryID       string `json:"ticket_memory_snapshot_id,omitempty"`
	RepoMemoryID         string `json:"repo_memory_snapshot_id,omitempty"`
	AssignmentID         string `json:"assignment_id,omitempty"`
	CurrentIssueID       string `json:"current_issue_id,omitempty"`
	CollaborationVersion int    `json:"collaboration_version,omitempty"`
}

func EncodeTaskContext(ctx TaskContext) ([]byte, error) {
	return json.Marshal(ctx)
}

func DecodeTaskContext(raw []byte) (TaskContext, error) {
	if len(raw) == 0 {
		return TaskContext{}, nil
	}
	var ctx TaskContext
	if err := json.Unmarshal(raw, &ctx); err != nil {
		return TaskContext{}, err
	}
	return ctx, nil
}

type HandoffPayload struct {
	Summary       string   `json:"summary"`
	WorkedOn      []string `json:"worked_on"`
	Evidence      []string `json:"evidence"`
	Validation    []string `json:"validation"`
	RemainingWork []string `json:"remaining_work"`
	HandoffNotes  []string `json:"handoff_notes"`
}

type OrchestratorOutput struct {
	Brief        string           `json:"brief"`
	Assignments  []AssignmentSpec `json:"assignments"`
	Dependencies []string         `json:"dependencies"`
	SharedNotes  []string         `json:"shared_notes"`
	NextSteps    []string         `json:"next_steps"`
}

type AssignmentSpec struct {
	IssueID         string   `json:"issue_id,omitempty"`
	AgentID         string   `json:"agent_id,omitempty"`
	Role            string   `json:"role,omitempty"`
	Goal            string   `json:"goal"`
	Context         string   `json:"context,omitempty"`
	OwnedScope      []string `json:"owned_scope,omitempty"`
	Inputs          []string `json:"inputs,omitempty"`
	ExpectedHandoff []string `json:"expected_handoff,omitempty"`
}

type PromptContext struct {
	Role           string            `json:"role,omitempty"`
	WorkroomID     string            `json:"workroom_id,omitempty"`
	AssignmentID   string            `json:"assignment_id,omitempty"`
	TaskBrief      map[string]any    `json:"task_brief,omitempty"`
	TicketMemory   json.RawMessage   `json:"ticket_memory,omitempty"`
	RepoMemory     json.RawMessage   `json:"repo_memory,omitempty"`
	Assignment     map[string]any    `json:"assignment,omitempty"`
	Assignments    []map[string]any  `json:"assignments,omitempty"`
	RecentHandoffs []json.RawMessage `json:"recent_handoffs,omitempty"`
	Events         []json.RawMessage `json:"events,omitempty"`
}
