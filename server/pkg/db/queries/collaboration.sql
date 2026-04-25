-- name: UpsertCollaborationWorkroom :one
INSERT INTO collaboration_workroom (workspace_id, issue_id, goal, current_summary)
VALUES ($1, $2, $3, $4)
ON CONFLICT (issue_id) DO UPDATE SET
    goal = CASE
        WHEN collaboration_workroom.goal = '' AND EXCLUDED.goal <> '' THEN EXCLUDED.goal
        ELSE collaboration_workroom.goal
    END,
    current_summary = CASE
        WHEN EXCLUDED.current_summary <> '' THEN EXCLUDED.current_summary
        ELSE collaboration_workroom.current_summary
    END,
    updated_at = now()
RETURNING *;

-- name: GetCollaborationWorkroomByIssue :one
SELECT * FROM collaboration_workroom
WHERE issue_id = $1;

-- name: GetCollaborationWorkroom :one
SELECT * FROM collaboration_workroom
WHERE id = $1;

-- name: CreateCollaborationMemorySnapshot :one
INSERT INTO collaboration_memory_snapshot (workroom_id, kind, payload)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ListCollaborationMemorySnapshots :many
SELECT * FROM collaboration_memory_snapshot
WHERE workroom_id = $1
ORDER BY created_at DESC;

-- name: GetLatestCollaborationMemorySnapshotByKind :one
SELECT * FROM collaboration_memory_snapshot
WHERE workroom_id = $1 AND kind = $2
ORDER BY created_at DESC
LIMIT 1;

-- name: CreateCollaborationAssignment :one
INSERT INTO collaboration_assignment (
    workroom_id, target_issue_id, agent_id, role, goal, context,
    owned_scope, inputs, expected_handoff
) VALUES (
    sqlc.arg(workroom_id),
    sqlc.narg(target_issue_id),
    sqlc.narg(agent_id),
    sqlc.arg(role),
    sqlc.arg(goal),
    sqlc.arg(context),
    sqlc.arg(owned_scope),
    sqlc.arg(inputs),
    sqlc.arg(expected_handoff)
)
RETURNING *;

-- name: SetCollaborationAssignmentTask :one
UPDATE collaboration_assignment
SET task_id = $2, status = 'running', updated_at = now()
WHERE id = $1
RETURNING *;

-- name: GetCollaborationAssignment :one
SELECT * FROM collaboration_assignment
WHERE id = $1;

-- name: GetCollaborationAssignmentByTask :one
SELECT * FROM collaboration_assignment
WHERE task_id = $1;

-- name: ListCollaborationAssignments :many
SELECT * FROM collaboration_assignment
WHERE workroom_id = $1
ORDER BY created_at ASC;

-- name: CreateCollaborationHandoff :one
INSERT INTO collaboration_handoff (
    workroom_id, assignment_id, task_id, agent_id, summary,
    worked_on, evidence, validation, remaining_work, handoff_notes, raw_payload
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: ListCollaborationHandoffs :many
SELECT * FROM collaboration_handoff
WHERE workroom_id = $1
ORDER BY created_at ASC;

-- name: MarkCollaborationAssignmentHandoffSubmitted :one
UPDATE collaboration_assignment
SET status = 'handoff_submitted', updated_at = now()
WHERE id = $1
RETURNING *;

-- name: MarkCollaborationAssignmentCancelled :one
UPDATE collaboration_assignment
SET status = 'cancelled', updated_at = now()
WHERE id = $1
RETURNING *;

-- name: CreateCollaborationEvent :one
INSERT INTO collaboration_event (workroom_id, event_type, actor_agent_id, task_id, payload)
VALUES ($1, $2, sqlc.narg(actor_agent_id), sqlc.narg(task_id), $3)
RETURNING *;

-- name: ListCollaborationEvents :many
SELECT * FROM collaboration_event
WHERE workroom_id = $1
ORDER BY created_at ASC;
