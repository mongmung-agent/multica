ALTER TABLE collaboration_assignment
    ADD COLUMN IF NOT EXISTS target_issue_id UUID REFERENCES issue(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_collaboration_assignment_target_issue
    ON collaboration_assignment(target_issue_id);
