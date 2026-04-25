export interface CollaborationAssignment {
  id: string;
  workroom_id: string;
  target_issue_id: string;
  task_id: string;
  agent_id: string;
  role: "orchestrator" | "worker";
  goal: string;
  context: string;
  owned_scope: string[];
  inputs: string[];
  expected_handoff: string[];
  status: "created" | "running" | "handoff_submitted" | "synthesized" | "cancelled";
}

export interface CollaborationMetrics {
  assignment_total: number;
  assignment_running: number;
  assignment_handoff_submitted: number;
  assignment_cancelled: number;
  handoff_count: number;
  synthesis_count: number;
  invalid_assignment_count: number;
  enqueue_failure_count: number;
  missing_handoff_count: number;
  continuation_rate: number;
}

export interface CollaborationRepoMemory {
  worked_on?: string[];
  evidence?: string[];
  validation?: string[];
  remaining_work?: string[];
  handoff_notes?: string[];
  synthesis_briefs?: string[];
  dependencies?: string[];
  shared_notes?: string[];
  next_steps?: string[];
  last_update_source?: string;
  last_updated_by_agent_id?: string;
  last_updated_task_id?: string;
}

export interface CollaborationContext {
  role?: "orchestrator" | "worker";
  workroom_id?: string;
  assignment_id?: string;
  task_brief?: Record<string, unknown>;
  ticket_memory?: Record<string, unknown>;
  repo_memory?: Record<string, unknown>;
  repo_memory_wiki?: CollaborationRepoMemory;
  metrics?: CollaborationMetrics;
  assignment?: CollaborationAssignment;
  assignments?: CollaborationAssignment[];
  recent_handoffs?: Record<string, unknown>[];
  events?: Record<string, unknown>[];
}

export interface IssueCollaborationResponse {
  collaboration: CollaborationContext | null;
}
