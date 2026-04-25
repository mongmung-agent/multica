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

	if taskContext.Role == collab.RoleWorker {
		handoff, raw := parseHandoffPayload(payload.Output)
		if strings.TrimSpace(handoff.Summary) == "" {
			handoff.Summary = strings.TrimSpace(payload.Output)
		}
		if strings.TrimSpace(handoff.Summary) == "" {
			handoff.Summary = "Task completed without a structured handoff summary."
		}
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
		s.updateRepoMemoryFromHandoff(ctx, workroomID, task.AgentID, task.ID, handoff)
		return nil, nil
	}

	orchestratorOutput, raw := parseOrchestratorOutput(payload.Output)
	s.updateRepoMemoryFromSynthesis(ctx, workroomID, task.AgentID, task.ID, orchestratorOutput)
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
		if reason := invalidAssignmentReason(spec); reason != "" {
			s.recordAssignmentQuestion(ctx, workroomID, task.AgentID, task.ID, reason, spec, "orchestrator_output")
			continue
		}
		assignment, err := s.createAssignmentFromSpec(ctx, workroomID, spec)
		if err != nil {
			s.recordAssignmentQuestion(ctx, workroomID, task.AgentID, task.ID, err.Error(), spec, "routing_policy")
			continue
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

func (s *CollaborationService) MarkAssignmentEnqueueFailed(ctx context.Context, assignment db.CollaborationAssignment, reason string) {
	_, _ = s.Queries.MarkCollaborationAssignmentCancelled(ctx, assignment.ID)
	_, _ = s.Queries.CreateCollaborationEvent(ctx, db.CreateCollaborationEventParams{
		WorkroomID: assignment.WorkroomID,
		EventType:  collab.EventQuestionRaised,
		Payload: mustJSON(map[string]any{
			"assignment_id": util.UUIDToString(assignment.ID),
			"reason":        reason,
			"source":        "assignment_enqueue",
		}),
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
	if repoMemory, err := s.Queries.GetCollaborationRepoMemory(ctx, workroomID); err == nil {
		pc.RepoMemoryWiki = json.RawMessage(repoMemory.Payload)
	}
	pc.Metrics = s.collaborationMetrics(ctx, workroomID)
	if taskContext.AssignmentID != "" {
		if assignment, err := s.Queries.GetCollaborationAssignment(ctx, util.ParseUUID(taskContext.AssignmentID)); err == nil {
			pc.Assignment = assignmentPayload(assignment)
		}
	}
	assignments, _ := s.Queries.ListCollaborationAssignments(ctx, workroomID)
	for _, assignment := range assignments {
		pc.Assignments = append(pc.Assignments, assignmentPayload(assignment))
	}
	handoffs, _ := s.Queries.ListCollaborationHandoffs(ctx, workroomID)
	for _, handoff := range handoffs {
		pc.RecentHandoffs = append(pc.RecentHandoffs, json.RawMessage(handoff.RawPayload))
	}
	events, _ := s.Queries.ListCollaborationEvents(ctx, workroomID)
	for _, event := range events {
		pc.Events = append(pc.Events, eventPayload(event))
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
	if repoMemory, err := s.Queries.GetCollaborationRepoMemory(ctx, workroom.ID); err == nil {
		pc.RepoMemoryWiki = json.RawMessage(repoMemory.Payload)
	}
	pc.Metrics = s.collaborationMetrics(ctx, workroom.ID)
	assignments, _ := s.Queries.ListCollaborationAssignments(ctx, workroom.ID)
	for _, assignment := range assignments {
		pc.Assignments = append(pc.Assignments, assignmentPayload(assignment))
	}
	handoffs, _ := s.Queries.ListCollaborationHandoffs(ctx, workroom.ID)
	for _, handoff := range handoffs {
		pc.RecentHandoffs = append(pc.RecentHandoffs, json.RawMessage(handoff.RawPayload))
	}
	events, _ := s.Queries.ListCollaborationEvents(ctx, workroom.ID)
	for _, event := range events {
		pc.Events = append(pc.Events, eventPayload(event))
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
	targetIssueID := util.ParseUUID(spec.IssueID)
	targetIssue, err := s.Queries.GetIssue(ctx, targetIssueID)
	if err != nil {
		return db.CollaborationAssignment{}, fmt.Errorf("load assignment target issue: %w", err)
	}
	agentID, err := s.resolveAssignmentAgent(ctx, spec, targetIssue)
	if err != nil {
		return db.CollaborationAssignment{}, err
	}
	role := spec.Role
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
		AgentID:         agentID,
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
		ActorAgentID: agentID,
		Payload: mustJSON(map[string]any{
			"assignment_id": util.UUIDToString(assignment.ID),
			"role":          role,
			"goal":          assignment.Goal,
			"source":        "orchestrator_output",
			"routing":       assignmentRoutingPayload(spec, targetIssue, agentID),
		}),
	})
	return assignment, nil
}

func (s *CollaborationService) resolveAssignmentAgent(ctx context.Context, spec collab.AssignmentSpec, targetIssue db.Issue) (pgtype.UUID, error) {
	agentID := util.ParseUUID(spec.AgentID)
	source := "assignment.agent_id"
	if !agentID.Valid {
		if targetIssue.AssigneeType.Valid && targetIssue.AssigneeType.String == "agent" && targetIssue.AssigneeID.Valid {
			agentID = targetIssue.AssigneeID
			source = "target_issue.assignee"
		} else {
			return pgtype.UUID{}, fmt.Errorf("routing failed: no runnable agent; assignment has no agent_id and target issue has no agent assignee")
		}
	}
	agent, err := s.Queries.GetAgent(ctx, agentID)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("routing failed: load agent from %s: %w", source, err)
	}
	if agent.ArchivedAt.Valid {
		return pgtype.UUID{}, fmt.Errorf("routing failed: agent from %s is archived", source)
	}
	if !agent.RuntimeID.Valid {
		return pgtype.UUID{}, fmt.Errorf("routing failed: agent from %s has no runtime", source)
	}
	return agentID, nil
}

func assignmentRoutingPayload(spec collab.AssignmentSpec, targetIssue db.Issue, agentID pgtype.UUID) map[string]any {
	source := "assignment.agent_id"
	if strings.TrimSpace(spec.AgentID) == "" {
		source = "target_issue.assignee"
	}
	return map[string]any{
		"source":          source,
		"agent_id":        util.UUIDToString(agentID),
		"target_issue_id": util.UUIDToString(targetIssue.ID),
	}
}

func invalidAssignmentReason(spec collab.AssignmentSpec) string {
	if !util.ParseUUID(spec.IssueID).Valid {
		return "assignment missing valid issue_id"
	}
	if spec.Role != collab.RoleWorker && spec.Role != collab.RoleOrchestrator {
		return "assignment role must be worker or orchestrator"
	}
	if strings.TrimSpace(spec.Goal) == "" {
		return "assignment missing goal"
	}
	return ""
}

func (s *CollaborationService) recordAssignmentQuestion(ctx context.Context, workroomID, actorAgentID, taskID pgtype.UUID, reason string, spec collab.AssignmentSpec, source string) {
	_, _ = s.Queries.CreateCollaborationEvent(ctx, db.CreateCollaborationEventParams{
		WorkroomID:   workroomID,
		EventType:    collab.EventQuestionRaised,
		ActorAgentID: actorAgentID,
		TaskID:       taskID,
		Payload: mustJSON(map[string]any{
			"reason":     reason,
			"assignment": spec,
			"source":     source,
		}),
	})
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

func parseOrchestratorOutput(output string) (collab.OrchestratorOutput, []byte) {
	var parsed collab.OrchestratorOutput
	output = strings.TrimSpace(output)
	if output == "" {
		return collab.OrchestratorOutput{}, mustJSON(collab.OrchestratorOutput{})
	}
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		return collab.OrchestratorOutput{
			Brief: output,
		}, mustJSON(collab.OrchestratorOutput{Brief: output})
	}
	return parsed, []byte(output)
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

func assignmentPayload(assignment db.CollaborationAssignment) map[string]any {
	return map[string]any{
		"id":               util.UUIDToString(assignment.ID),
		"workroom_id":      util.UUIDToString(assignment.WorkroomID),
		"target_issue_id":  util.UUIDToString(assignment.TargetIssueID),
		"task_id":          util.UUIDToString(assignment.TaskID),
		"agent_id":         util.UUIDToString(assignment.AgentID),
		"role":             assignment.Role,
		"goal":             assignment.Goal,
		"context":          assignment.Context,
		"owned_scope":      json.RawMessage(assignment.OwnedScope),
		"inputs":           json.RawMessage(assignment.Inputs),
		"expected_handoff": json.RawMessage(assignment.ExpectedHandoff),
		"status":           assignment.Status,
	}
}

func eventPayload(event db.CollaborationEvent) json.RawMessage {
	payload := map[string]any{}
	_ = json.Unmarshal(event.Payload, &payload)
	payload["id"] = util.UUIDToString(event.ID)
	payload["event_type"] = event.EventType
	payload["created_at"] = event.CreatedAt.Time
	if event.ActorAgentID.Valid {
		payload["actor_agent_id"] = util.UUIDToString(event.ActorAgentID)
	}
	if event.TaskID.Valid {
		payload["task_id"] = util.UUIDToString(event.TaskID)
	}
	return mustJSON(payload)
}

func (s *CollaborationService) updateRepoMemoryFromHandoff(ctx context.Context, workroomID, agentID, taskID pgtype.UUID, handoff collab.HandoffPayload) {
	memory := s.loadRepoMemoryWiki(ctx, workroomID)
	appendStrings(memory, "worked_on", handoff.WorkedOn)
	appendStrings(memory, "evidence", handoff.Evidence)
	appendStrings(memory, "validation", handoff.Validation)
	appendStrings(memory, "remaining_work", handoff.RemainingWork)
	appendStrings(memory, "handoff_notes", handoff.HandoffNotes)
	memory["last_update_source"] = "worker_handoff"
	memory["last_updated_by_agent_id"] = util.UUIDToString(agentID)
	memory["last_updated_task_id"] = util.UUIDToString(taskID)
	_, _ = s.Queries.UpsertCollaborationRepoMemory(ctx, db.UpsertCollaborationRepoMemoryParams{
		WorkroomID:       workroomID,
		Payload:          mustJSON(memory),
		UpdatedByAgentID: agentID,
		UpdatedTaskID:    taskID,
	})
}

func (s *CollaborationService) updateRepoMemoryFromSynthesis(ctx context.Context, workroomID, agentID, taskID pgtype.UUID, output collab.OrchestratorOutput) {
	memory := s.loadRepoMemoryWiki(ctx, workroomID)
	appendStrings(memory, "synthesis_briefs", []string{output.Brief})
	appendStrings(memory, "dependencies", output.Dependencies)
	appendStrings(memory, "shared_notes", output.SharedNotes)
	appendStrings(memory, "next_steps", output.NextSteps)
	memory["last_update_source"] = "orchestrator_synthesis"
	memory["last_updated_by_agent_id"] = util.UUIDToString(agentID)
	memory["last_updated_task_id"] = util.UUIDToString(taskID)
	_, _ = s.Queries.UpsertCollaborationRepoMemory(ctx, db.UpsertCollaborationRepoMemoryParams{
		WorkroomID:       workroomID,
		Payload:          mustJSON(memory),
		UpdatedByAgentID: agentID,
		UpdatedTaskID:    taskID,
	})
}

func (s *CollaborationService) loadRepoMemoryWiki(ctx context.Context, workroomID pgtype.UUID) map[string]any {
	base := map[string]any{
		"worked_on":        []string{},
		"evidence":         []string{},
		"validation":       []string{},
		"remaining_work":   []string{},
		"handoff_notes":    []string{},
		"synthesis_briefs": []string{},
		"dependencies":     []string{},
		"shared_notes":     []string{},
		"next_steps":       []string{},
	}
	row, err := s.Queries.GetCollaborationRepoMemory(ctx, workroomID)
	if err != nil {
		return base
	}
	var decoded map[string]any
	if err := json.Unmarshal(row.Payload, &decoded); err != nil {
		return base
	}
	for key, value := range decoded {
		base[key] = value
	}
	return base
}

func appendStrings(memory map[string]any, key string, values []string) {
	existing := stringSlice(memory[key])
	seen := make(map[string]bool, len(existing)+len(values))
	for _, value := range existing {
		seen[value] = true
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		existing = append(existing, value)
		seen[value] = true
	}
	if len(existing) > 50 {
		existing = existing[len(existing)-50:]
	}
	memory[key] = existing
}

func stringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string{}, typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return []string{}
	}
}

func (s *CollaborationService) collaborationMetrics(ctx context.Context, workroomID pgtype.UUID) map[string]any {
	assignments, _ := s.Queries.ListCollaborationAssignments(ctx, workroomID)
	handoffs, _ := s.Queries.ListCollaborationHandoffs(ctx, workroomID)
	events, _ := s.Queries.ListCollaborationEvents(ctx, workroomID)
	metrics := map[string]any{
		"assignment_total":             len(assignments),
		"assignment_running":           0,
		"assignment_handoff_submitted": 0,
		"assignment_cancelled":         0,
		"handoff_count":                len(handoffs),
		"synthesis_count":              0,
		"invalid_assignment_count":     0,
		"enqueue_failure_count":        0,
		"missing_handoff_count":        0,
		"continuation_rate":            0.0,
	}
	for _, assignment := range assignments {
		switch assignment.Status {
		case "running":
			metrics["assignment_running"] = metrics["assignment_running"].(int) + 1
			if assignment.Role == collab.RoleWorker {
				metrics["missing_handoff_count"] = metrics["missing_handoff_count"].(int) + 1
			}
		case "handoff_submitted":
			metrics["assignment_handoff_submitted"] = metrics["assignment_handoff_submitted"].(int) + 1
		case "cancelled":
			metrics["assignment_cancelled"] = metrics["assignment_cancelled"].(int) + 1
		}
	}
	for _, event := range events {
		switch event.EventType {
		case collab.EventOrchestratorSynthesisAdded:
			metrics["synthesis_count"] = metrics["synthesis_count"].(int) + 1
		case collab.EventQuestionRaised:
			var payload map[string]any
			_ = json.Unmarshal(event.Payload, &payload)
			source, _ := payload["source"].(string)
			if source == "assignment_enqueue" {
				metrics["enqueue_failure_count"] = metrics["enqueue_failure_count"].(int) + 1
			} else {
				metrics["invalid_assignment_count"] = metrics["invalid_assignment_count"].(int) + 1
			}
		}
	}
	if len(assignments) > 0 {
		metrics["continuation_rate"] = float64(len(handoffs)) / float64(len(assignments))
	}
	return metrics
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
