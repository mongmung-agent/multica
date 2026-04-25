package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	collab "github.com/multica-ai/multica/server/internal/collaboration"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type CollaborationService struct {
	Queries *db.Queries
}

func NewCollaborationService(q *db.Queries) *CollaborationService {
	return &CollaborationService{Queries: q}
}

func (s *CollaborationService) PrepareIssueTask(ctx context.Context, issue db.Issue, agentID pgtype.UUID, triggerCommentID pgtype.UUID) ([]byte, db.CollaborationAssignment, error) {
	workroomIssue := issue
	if issue.ParentIssueID.Valid {
		parent, err := s.Queries.GetIssue(ctx, issue.ParentIssueID)
		if err != nil {
			return nil, db.CollaborationAssignment{}, fmt.Errorf("load parent issue for workroom: %w", err)
		}
		workroomIssue = parent
	}

	workroom, err := s.ensureWorkroom(ctx, workroomIssue)
	if err != nil {
		return nil, db.CollaborationAssignment{}, err
	}

	ticketMemory, err := s.createTicketMemorySnapshot(ctx, workroom, issue, triggerCommentID)
	if err != nil {
		return nil, db.CollaborationAssignment{}, err
	}
	repoMemory, err := s.createRepoMemorySnapshot(ctx, workroom)
	if err != nil {
		return nil, db.CollaborationAssignment{}, err
	}

	role := collab.RoleOrchestrator
	if issue.ParentIssueID.Valid {
		role = collab.RoleWorker
	}
	assignment, err := s.createAssignment(ctx, workroom, issue, agentID, role)
	if err != nil {
		return nil, db.CollaborationAssignment{}, err
	}

	taskContext, err := collab.EncodeTaskContext(collab.TaskContext{
		Role:                 role,
		WorkroomID:           util.UUIDToString(workroom.ID),
		TicketMemoryID:       util.UUIDToString(ticketMemory.ID),
		RepoMemoryID:         util.UUIDToString(repoMemory.ID),
		AssignmentID:         util.UUIDToString(assignment.ID),
		CurrentIssueID:       util.UUIDToString(issue.ID),
		CollaborationVersion: 1,
	})
	if err != nil {
		return nil, db.CollaborationAssignment{}, err
	}
	return taskContext, assignment, nil
}

func (s *CollaborationService) AttachTaskToAssignment(ctx context.Context, assignmentID, taskID pgtype.UUID) error {
	if _, err := s.Queries.SetCollaborationAssignmentTask(ctx, db.SetCollaborationAssignmentTaskParams{
		ID:     assignmentID,
		TaskID: taskID,
	}); err != nil {
		return fmt.Errorf("attach task to collaboration assignment: %w", err)
	}
	return nil
}

func (s *CollaborationService) RecordTaskCompletion(ctx context.Context, task db.AgentTaskQueue, payload protocol.TaskCompletedPayload) ([]db.CollaborationAssignment, error) {
	if !task.IssueID.Valid || len(task.Context) == 0 {
		return nil, nil
	}
	taskContext, err := collab.DecodeTaskContext(task.Context)
	if err != nil || taskContext.WorkroomID == "" || taskContext.AssignmentID == "" {
		return nil, nil
	}
	workroomID := util.ParseUUID(taskContext.WorkroomID)
	assignmentID := util.ParseUUID(taskContext.AssignmentID)
	if !workroomID.Valid || !assignmentID.Valid {
		return nil, nil
	}

	handoff, raw := parseHandoffPayload(payload.Output)
	if strings.TrimSpace(handoff.Summary) == "" {
		handoff.Summary = strings.TrimSpace(payload.Output)
	}
	if strings.TrimSpace(handoff.Summary) == "" {
		handoff.Summary = "Task completed without a structured handoff summary."
	}

	if taskContext.Role == collab.RoleWorker {
		if _, err := s.Queries.CreateCollaborationHandoff(ctx, db.CreateCollaborationHandoffParams{
			WorkroomID:    workroomID,
			AssignmentID:  assignmentID,
			TaskID:        task.ID,
			AgentID:       task.AgentID,
			Summary:       handoff.Summary,
			WorkedOn:      mustJSON(handoff.WorkedOn),
			Evidence:      mustJSON(handoff.Evidence),
			Validation:    mustJSON(handoff.Validation),
			RemainingWork: mustJSON(handoff.RemainingWork),
			HandoffNotes:  mustJSON(handoff.HandoffNotes),
			RawPayload:    raw,
		}); err != nil {
			return nil, fmt.Errorf("record worker handoff: %w", err)
		}
		_, _ = s.Queries.MarkCollaborationAssignmentHandoffSubmitted(ctx, assignmentID)
		_, _ = s.Queries.CreateCollaborationEvent(ctx, db.CreateCollaborationEventParams{
			WorkroomID:   workroomID,
			EventType:    collab.EventWorkerHandoffAdded,
			ActorAgentID: task.AgentID,
			TaskID:       task.ID,
			Payload:      raw,
		})
		return nil, nil
	}

	orchestratorOutput := parseOrchestratorOutput(payload.Output)
	var createdAssignments []db.CollaborationAssignment
	eventType := collab.EventBriefCreated
	if handoffs, err := s.Queries.ListCollaborationHandoffs(ctx, workroomID); err == nil && len(handoffs) > 0 {
		eventType = collab.EventOrchestratorSynthesisAdded
	}
	_, _ = s.Queries.CreateCollaborationEvent(ctx, db.CreateCollaborationEventParams{
		WorkroomID:   workroomID,
		EventType:    eventType,
		ActorAgentID: task.AgentID,
		TaskID:       task.ID,
		Payload:      raw,
	})
	for _, spec := range orchestratorOutput.Assignments {
		assignment, err := s.createAssignmentFromSpec(ctx, workroomID, spec)
		if err != nil {
			return createdAssignments, err
		}
		createdAssignments = append(createdAssignments, assignment)
	}
	return createdAssignments, nil
}

func (s *CollaborationService) TaskContextForAssignment(ctx context.Context, assignment db.CollaborationAssignment, issueID pgtype.UUID) ([]byte, error) {
	workroom, err := s.Queries.GetCollaborationWorkroom(ctx, assignment.WorkroomID)
	if err != nil {
		return nil, fmt.Errorf("load collaboration workroom: %w", err)
	}
	issue, err := s.Queries.GetIssue(ctx, issueID)
	if err != nil {
		return nil, fmt.Errorf("load collaboration assignment issue: %w", err)
	}
	ticketMemory, err := s.createTicketMemorySnapshot(ctx, workroom, issue, pgtype.UUID{})
	if err != nil {
		return nil, err
	}
	repoMemory, err := s.createRepoMemorySnapshot(ctx, workroom)
	if err != nil {
		return nil, err
	}
	return collab.EncodeTaskContext(collab.TaskContext{
		Role:                 assignment.Role,
		WorkroomID:           util.UUIDToString(assignment.WorkroomID),
		TicketMemoryID:       util.UUIDToString(ticketMemory.ID),
		RepoMemoryID:         util.UUIDToString(repoMemory.ID),
		AssignmentID:         util.UUIDToString(assignment.ID),
		CurrentIssueID:       util.UUIDToString(issueID),
		CollaborationVersion: 1,
	})
}

func (s *CollaborationService) PromptContext(ctx context.Context, task db.AgentTaskQueue) (*collab.PromptContext, error) {
	taskContext, err := collab.DecodeTaskContext(task.Context)
	if err != nil || taskContext.WorkroomID == "" {
		return nil, err
	}
	workroomID := util.ParseUUID(taskContext.WorkroomID)
	if !workroomID.Valid {
		return nil, nil
	}
	pc := &collab.PromptContext{
		Role:         taskContext.Role,
		WorkroomID:   taskContext.WorkroomID,
		AssignmentID: taskContext.AssignmentID,
	}
	if workroom, err := s.Queries.GetCollaborationWorkroom(ctx, workroomID); err == nil {
		pc.TaskBrief = map[string]any{
			"goal":            workroom.Goal,
			"current_summary": workroom.CurrentSummary,
			"issue_id":        util.UUIDToString(workroom.IssueID),
		}
	}
	if taskContext.TicketMemoryID != "" {
		if snapshot, err := s.Queries.GetLatestCollaborationMemorySnapshotByKind(ctx, db.GetLatestCollaborationMemorySnapshotByKindParams{
			WorkroomID: workroomID,
			Kind:       "ticket",
		}); err == nil {
			pc.TicketMemory = json.RawMessage(snapshot.Payload)
		}
	}
	if taskContext.RepoMemoryID != "" {
		if snapshot, err := s.Queries.GetLatestCollaborationMemorySnapshotByKind(ctx, db.GetLatestCollaborationMemorySnapshotByKindParams{
			WorkroomID: workroomID,
			Kind:       "repo",
		}); err == nil {
			pc.RepoMemory = json.RawMessage(snapshot.Payload)
		}
	}
	if taskContext.AssignmentID != "" {
		if assignment, err := s.Queries.GetCollaborationAssignment(ctx, util.ParseUUID(taskContext.AssignmentID)); err == nil {
			pc.Assignment = map[string]any{
				"role":             assignment.Role,
				"goal":             assignment.Goal,
				"context":          assignment.Context,
				"owned_scope":      json.RawMessage(assignment.OwnedScope),
				"inputs":           json.RawMessage(assignment.Inputs),
				"expected_handoff": json.RawMessage(assignment.ExpectedHandoff),
			}
		}
	}
	handoffs, _ := s.Queries.ListCollaborationHandoffs(ctx, workroomID)
	for _, handoff := range handoffs {
		pc.RecentHandoffs = append(pc.RecentHandoffs, json.RawMessage(handoff.RawPayload))
	}
	events, _ := s.Queries.ListCollaborationEvents(ctx, workroomID)
	for _, event := range events {
		pc.Events = append(pc.Events, json.RawMessage(event.Payload))
	}
	return pc, nil
}

func (s *CollaborationService) WorkroomSnapshot(ctx context.Context, issueID pgtype.UUID) (*collab.PromptContext, error) {
	workroom, err := s.Queries.GetCollaborationWorkroomByIssue(ctx, issueID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	pc := &collab.PromptContext{
		WorkroomID: util.UUIDToString(workroom.ID),
		TaskBrief: map[string]any{
			"goal":            workroom.Goal,
			"current_summary": workroom.CurrentSummary,
			"issue_id":        util.UUIDToString(workroom.IssueID),
		},
	}
	if snapshot, err := s.Queries.GetLatestCollaborationMemorySnapshotByKind(ctx, db.GetLatestCollaborationMemorySnapshotByKindParams{
		WorkroomID: workroom.ID,
		Kind:       "ticket",
	}); err == nil {
		pc.TicketMemory = json.RawMessage(snapshot.Payload)
	}
	if snapshot, err := s.Queries.GetLatestCollaborationMemorySnapshotByKind(ctx, db.GetLatestCollaborationMemorySnapshotByKindParams{
		WorkroomID: workroom.ID,
		Kind:       "repo",
	}); err == nil {
		pc.RepoMemory = json.RawMessage(snapshot.Payload)
	}
	handoffs, _ := s.Queries.ListCollaborationHandoffs(ctx, workroom.ID)
	for _, handoff := range handoffs {
		pc.RecentHandoffs = append(pc.RecentHandoffs, json.RawMessage(handoff.RawPayload))
	}
	events, _ := s.Queries.ListCollaborationEvents(ctx, workroom.ID)
	for _, event := range events {
		pc.Events = append(pc.Events, json.RawMessage(event.Payload))
	}
	return pc, nil
}

func (s *CollaborationService) ensureWorkroom(ctx context.Context, issue db.Issue) (db.CollaborationWorkroom, error) {
	summary := fmt.Sprintf("Collaboration workroom for issue %s", issue.Title)
	workroom, err := s.Queries.UpsertCollaborationWorkroom(ctx, db.UpsertCollaborationWorkroomParams{
		WorkspaceID:    issue.WorkspaceID,
		IssueID:        issue.ID,
		Goal:           issue.Title,
		CurrentSummary: summary,
	})
	if err != nil {
		return db.CollaborationWorkroom{}, fmt.Errorf("ensure collaboration workroom: %w", err)
	}
	_, _ = s.Queries.CreateCollaborationEvent(ctx, db.CreateCollaborationEventParams{
		WorkroomID: workroom.ID,
		EventType:  collab.EventBriefCreated,
		Payload: mustJSON(map[string]any{
			"goal":  workroom.Goal,
			"issue": util.UUIDToString(issue.ID),
		}),
	})
	return workroom, nil
}

func (s *CollaborationService) createTicketMemorySnapshot(ctx context.Context, workroom db.CollaborationWorkroom, currentIssue db.Issue, triggerCommentID pgtype.UUID) (db.CollaborationMemorySnapshot, error) {
	comments, _ := s.Queries.ListComments(ctx, db.ListCommentsParams{
		IssueID:     currentIssue.ID,
		WorkspaceID: currentIssue.WorkspaceID,
	})
	children, _ := s.Queries.ListChildIssues(ctx, workroom.IssueID)
	var trigger any
	if triggerCommentID.Valid {
		if comment, err := s.Queries.GetComment(ctx, triggerCommentID); err == nil {
			trigger = commentPayload(comment)
		}
	}
	payload := map[string]any{
		"current_issue":   issuePayload(currentIssue),
		"workroom_issue":  util.UUIDToString(workroom.IssueID),
		"trigger_comment": trigger,
		"comments":        commentsPayload(comments),
		"child_issues":    issuesPayload(children),
	}
	snapshot, err := s.Queries.CreateCollaborationMemorySnapshot(ctx, db.CreateCollaborationMemorySnapshotParams{
		WorkroomID: workroom.ID,
		Kind:       "ticket",
		Payload:    mustJSON(payload),
	})
	if err != nil {
		return db.CollaborationMemorySnapshot{}, fmt.Errorf("create ticket memory snapshot: %w", err)
	}
	_, _ = s.Queries.CreateCollaborationEvent(ctx, db.CreateCollaborationEventParams{
		WorkroomID: workroom.ID,
		EventType:  collab.EventMemorySnapshotAdded,
		Payload:    mustJSON(map[string]any{"kind": "ticket", "snapshot_id": util.UUIDToString(snapshot.ID)}),
	})
	return snapshot, nil
}

func (s *CollaborationService) createRepoMemorySnapshot(ctx context.Context, workroom db.CollaborationWorkroom) (db.CollaborationMemorySnapshot, error) {
	var repos any = []any{}
	if ws, err := s.Queries.GetWorkspace(ctx, workroom.WorkspaceID); err == nil && len(ws.Repos) > 0 {
		var decoded any
		if err := json.Unmarshal(ws.Repos, &decoded); err == nil {
			repos = decoded
		}
	}
	payload := map[string]any{
		"repos":                  repos,
		"guidance":               loadRepoGuidance(),
		"structure":              repoStructureMemory(),
		"validation_commands":    repoValidationCommands(),
		"package_boundaries":     repoPackageBoundaries(),
		"recurring_cautions":     repoRecurringCautions(),
		"purpose":                "Shared repo memory seed for collaboration. Agents should update handoff notes with concrete repo findings.",
		"collaboration_contract": "Use this repo memory as shared context; do not infer repo rules by substring-checking whether a prompt mentions AGENTS.md.",
	}
	snapshot, err := s.Queries.CreateCollaborationMemorySnapshot(ctx, db.CreateCollaborationMemorySnapshotParams{
		WorkroomID: workroom.ID,
		Kind:       "repo",
		Payload:    mustJSON(payload),
	})
	if err != nil {
		return db.CollaborationMemorySnapshot{}, fmt.Errorf("create repo memory snapshot: %w", err)
	}
	_, _ = s.Queries.CreateCollaborationEvent(ctx, db.CreateCollaborationEventParams{
		WorkroomID: workroom.ID,
		EventType:  collab.EventMemorySnapshotAdded,
		Payload:    mustJSON(map[string]any{"kind": "repo", "snapshot_id": util.UUIDToString(snapshot.ID)}),
	})
	return snapshot, nil
}

func (s *CollaborationService) createAssignment(ctx context.Context, workroom db.CollaborationWorkroom, issue db.Issue, agentID pgtype.UUID, role string) (db.CollaborationAssignment, error) {
	contextText := "Coordinate the shared workroom and keep collaboration moving."
	expected := []string{"brief", "assignments", "dependencies", "shared_notes", "next_steps"}
	if role == collab.RoleWorker {
		contextText = "Complete this issue as a worker in the parent issue workroom, then leave a handoff another agent can continue from."
		expected = []string{"summary", "worked_on", "evidence", "validation", "remaining_work", "handoff_notes"}
	}
	assignment, err := s.Queries.CreateCollaborationAssignment(ctx, db.CreateCollaborationAssignmentParams{
		WorkroomID:      workroom.ID,
		TargetIssueID:   issue.ID,
		AgentID:         agentID,
		Role:            role,
		Goal:            issue.Title,
		Context:         contextText,
		OwnedScope:      mustJSON([]string{}),
		Inputs:          mustJSON([]string{"ticket_memory", "repo_memory", "previous_handoffs"}),
		ExpectedHandoff: mustJSON(expected),
	})
	if err != nil {
		return db.CollaborationAssignment{}, fmt.Errorf("create collaboration assignment: %w", err)
	}
	_, _ = s.Queries.CreateCollaborationEvent(ctx, db.CreateCollaborationEventParams{
		WorkroomID:   workroom.ID,
		EventType:    collab.EventAssignmentCreated,
		ActorAgentID: agentID,
		Payload: mustJSON(map[string]any{
			"assignment_id": util.UUIDToString(assignment.ID),
			"role":          role,
			"goal":          assignment.Goal,
		}),
	})
	return assignment, nil
}

func (s *CollaborationService) createAssignmentFromSpec(ctx context.Context, workroomID pgtype.UUID, spec collab.AssignmentSpec) (db.CollaborationAssignment, error) {
	workroom, err := s.Queries.GetCollaborationWorkroom(ctx, workroomID)
	if err != nil {
		return db.CollaborationAssignment{}, fmt.Errorf("load collaboration workroom for assignment: %w", err)
	}
	targetIssueID := util.ParseUUID(spec.IssueID)
	if !targetIssueID.Valid {
		targetIssueID = workroom.IssueID
	}
	role := spec.Role
	if role != collab.RoleOrchestrator && role != collab.RoleWorker {
		role = collab.RoleWorker
	}
	contextText := spec.Context
	if strings.TrimSpace(contextText) == "" {
		contextText = "Continue the shared collaboration workroom and leave a handoff another agent can use."
	}
	expected := spec.ExpectedHandoff
	if len(expected) == 0 {
		expected = []string{"summary", "worked_on", "evidence", "validation", "remaining_work", "handoff_notes"}
	}
	assignment, err := s.Queries.CreateCollaborationAssignment(ctx, db.CreateCollaborationAssignmentParams{
		WorkroomID:      workroomID,
		TargetIssueID:   targetIssueID,
		AgentID:         util.ParseUUID(spec.AgentID),
		Role:            role,
		Goal:            strings.TrimSpace(spec.Goal),
		Context:         contextText,
		OwnedScope:      mustJSON(spec.OwnedScope),
		Inputs:          mustJSON(spec.Inputs),
		ExpectedHandoff: mustJSON(expected),
	})
	if err != nil {
		return db.CollaborationAssignment{}, fmt.Errorf("create collaboration assignment from orchestrator output: %w", err)
	}
	_, _ = s.Queries.CreateCollaborationEvent(ctx, db.CreateCollaborationEventParams{
		WorkroomID:   workroomID,
		EventType:    collab.EventAssignmentCreated,
		ActorAgentID: util.ParseUUID(spec.AgentID),
		Payload: mustJSON(map[string]any{
			"assignment_id": util.UUIDToString(assignment.ID),
			"role":          role,
			"goal":          assignment.Goal,
			"source":        "orchestrator_output",
		}),
	})
	return assignment, nil
}

func parseHandoffPayload(output string) (collab.HandoffPayload, []byte) {
	output = strings.TrimSpace(output)
	if output == "" {
		raw := mustJSON(map[string]any{})
		return collab.HandoffPayload{}, raw
	}
	var payload collab.HandoffPayload
	if err := json.Unmarshal([]byte(output), &payload); err == nil {
		return payload, []byte(output)
	}
	payload.Summary = output
	raw := mustJSON(map[string]any{"summary": output})
	return payload, raw
}

func parseOrchestratorOutput(output string) collab.OrchestratorOutput {
	var parsed collab.OrchestratorOutput
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &parsed); err != nil {
		return collab.OrchestratorOutput{}
	}
	return parsed
}

func issuePayload(issue db.Issue) map[string]any {
	return map[string]any{
		"id":              util.UUIDToString(issue.ID),
		"workspace_id":    util.UUIDToString(issue.WorkspaceID),
		"title":           issue.Title,
		"description":     issue.Description.String,
		"status":          issue.Status,
		"priority":        issue.Priority,
		"assignee_type":   issue.AssigneeType.String,
		"assignee_id":     util.UUIDToString(issue.AssigneeID),
		"parent_issue_id": util.UUIDToString(issue.ParentIssueID),
		"number":          issue.Number,
	}
}

func issuesPayload(issues []db.Issue) []map[string]any {
	out := make([]map[string]any, 0, len(issues))
	for _, issue := range issues {
		out = append(out, issuePayload(issue))
	}
	return out
}

func commentPayload(comment db.Comment) map[string]any {
	return map[string]any{
		"id":          util.UUIDToString(comment.ID),
		"author_type": comment.AuthorType,
		"author_id":   util.UUIDToString(comment.AuthorID),
		"content":     comment.Content,
		"type":        comment.Type,
		"parent_id":   util.UUIDToString(comment.ParentID),
		"created_at":  comment.CreatedAt.Time,
	}
}

func commentsPayload(comments []db.Comment) []map[string]any {
	out := make([]map[string]any, 0, len(comments))
	for _, comment := range comments {
		out = append(out, commentPayload(comment))
	}
	return out
}

func mustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		return []byte("{}")
	}
	return data
}

func loadRepoGuidance() map[string]any {
	root, found := findRepoRoot()
	files := []string{"AGENTS.md", "CLAUDE.md", "README.md"}
	out := map[string]any{
		"repo_root": root,
		"files":     []map[string]any{},
	}
	entries := make([]map[string]any, 0, len(files))
	for _, name := range files {
		entry := map[string]any{
			"path":  name,
			"found": false,
		}
		if found {
			if content, err := os.ReadFile(filepath.Join(root, name)); err == nil {
				entry["found"] = true
				entry["content"] = truncateForMemory(string(content), 20000)
			}
		}
		entries = append(entries, entry)
	}
	out["files"] = entries
	return out
}

func findRepoRoot() (string, bool) {
	wd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	dir := filepath.Clean(wd)
	for range 8 {
		if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); err == nil {
			if info, err := os.Stat(filepath.Join(dir, "server")); err == nil && info.IsDir() {
				return dir, true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", false
}

func truncateForMemory(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return value[:max] + "\n...[truncated]"
}

func repoStructureMemory() []string {
	return []string{
		"server/: Go backend with Chi router, sqlc queries, gorilla/websocket realtime paths.",
		"apps/web/: Next.js App Router frontend.",
		"apps/desktop/: Electron desktop app.",
		"packages/core/: headless business logic, Zustand stores, React Query hooks, API client.",
		"packages/ui/: atomic UI components with zero business logic.",
		"packages/views/: shared business pages/components.",
	}
}

func repoValidationCommands() []string {
	return []string{
		"make check",
		"make test",
		"pnpm typecheck",
		"pnpm test",
		"cd server && go test ./...",
		"make sqlc",
	}
}

func repoPackageBoundaries() []string {
	return []string{
		"React Query owns server state; Zustand owns client state.",
		"packages/core has zero react-dom, zero localStorage, zero process.env.",
		"packages/ui has zero @multica/core imports.",
		"packages/views has zero next/* and zero react-router-dom imports.",
		"apps/web/platform is the only place for Next.js APIs.",
		"WS events invalidate React Query instead of writing directly to stores.",
	}
}

func repoRecurringCautions() []string {
	return []string{
		"Read CLAUDE.md as the authoritative project guidance before code changes.",
		"Prefer existing local patterns over parallel abstractions.",
		"Do not use issue_workflow or issue_execution_state as the collaboration source of truth.",
		"Worker completion should produce a handoff that another agent can continue from.",
		"Keep comments in code English only.",
	}
}
