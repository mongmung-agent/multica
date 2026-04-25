DROP INDEX IF EXISTS idx_collaboration_assignment_target_issue;

ALTER TABLE collaboration_assignment
    DROP COLUMN IF EXISTS target_issue_id;
