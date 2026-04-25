CREATE TABLE IF NOT EXISTS collaboration_workroom (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    goal TEXT NOT NULL DEFAULT '',
    current_summary TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (issue_id)
);

CREATE INDEX IF NOT EXISTS idx_collaboration_workroom_workspace
    ON collaboration_workroom(workspace_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS collaboration_memory_snapshot (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workroom_id UUID NOT NULL REFERENCES collaboration_workroom(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('ticket', 'repo')),
    payload JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_collaboration_memory_workroom_created
    ON collaboration_memory_snapshot(workroom_id, created_at DESC);

CREATE TABLE IF NOT EXISTS collaboration_assignment (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workroom_id UUID NOT NULL REFERENCES collaboration_workroom(id) ON DELETE CASCADE,
    task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
    role TEXT NOT NULL CHECK (role IN ('orchestrator', 'worker')),
    goal TEXT NOT NULL DEFAULT '',
    context TEXT NOT NULL DEFAULT '',
    owned_scope JSONB NOT NULL DEFAULT '[]',
    inputs JSONB NOT NULL DEFAULT '[]',
    expected_handoff JSONB NOT NULL DEFAULT '[]',
    status TEXT NOT NULL DEFAULT 'created' CHECK (status IN ('created', 'running', 'handoff_submitted', 'synthesized', 'cancelled')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_collaboration_assignment_workroom_created
    ON collaboration_assignment(workroom_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_collaboration_assignment_task
    ON collaboration_assignment(task_id);

CREATE TABLE IF NOT EXISTS collaboration_handoff (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workroom_id UUID NOT NULL REFERENCES collaboration_workroom(id) ON DELETE CASCADE,
    assignment_id UUID REFERENCES collaboration_assignment(id) ON DELETE SET NULL,
    task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
    summary TEXT NOT NULL DEFAULT '',
    worked_on JSONB NOT NULL DEFAULT '[]',
    evidence JSONB NOT NULL DEFAULT '[]',
    validation JSONB NOT NULL DEFAULT '[]',
    remaining_work JSONB NOT NULL DEFAULT '[]',
    handoff_notes JSONB NOT NULL DEFAULT '[]',
    raw_payload JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_collaboration_handoff_workroom_created
    ON collaboration_handoff(workroom_id, created_at DESC);

CREATE TABLE IF NOT EXISTS collaboration_event (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workroom_id UUID NOT NULL REFERENCES collaboration_workroom(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL CHECK (event_type IN (
        'brief_created',
        'memory_snapshot_added',
        'assignment_created',
        'worker_handoff_added',
        'orchestrator_synthesis_added',
        'question_raised',
        'decision_recorded'
    )),
    actor_agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
    task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    payload JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_collaboration_event_workroom_created
    ON collaboration_event(workroom_id, created_at DESC);
