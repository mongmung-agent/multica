CREATE TABLE IF NOT EXISTS collaboration_repo_memory (
    workroom_id UUID PRIMARY KEY REFERENCES collaboration_workroom(id) ON DELETE CASCADE,
    payload JSONB NOT NULL DEFAULT '{}',
    updated_by_agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
    updated_task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
