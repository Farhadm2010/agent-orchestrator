-- +goose Up
-- Every foreign key into sessions, conversation_turns, conversation_branches
-- and review gets an index on its child column.
--
-- When a parent row is deleted, SQLite looks up the rows that reference it in
-- every child table, whatever the ON DELETE action (CASCADE, SET NULL,
-- RESTRICT, NO ACTION). Without an index on the child column that lookup is a
-- scan of the whole child table, once per deleted parent row. A session delete
-- deletes the session, every turn of its conversations and their reviews, so
-- it scanned change_log twice (100,345 rows on a real archive) and
-- conversation_turns several times per deleted turn, all inside the
-- transaction holding the daemon's only writer. The cost grew with the
-- database, not with the session.
--
-- Partial indexes leave out the NULL rows, which never match a lookup, so the
-- mostly-NULL link columns cost almost nothing to maintain.
--
-- One foreign key stays unindexed: conversation_turns.handled_by_session_id.
-- Without statistics SQLite would prefer that index to the branch and
-- conversation indexes four hot turn queries use (every turn a session ever
-- handled instead of one branch's), and its only lookup is one scan per
-- session delete, after the delete has removed that session's own turns.

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_change_log_session
    ON change_log (session_id) WHERE session_id IS NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_conversation_turns_retry_of
    ON conversation_turns (retry_of_turn_id) WHERE retry_of_turn_id IS NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_conversation_turns_promoted_to
    ON conversation_turns (promoted_to_turn_id) WHERE promoted_to_turn_id IS NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_conversation_turns_handled_by_review
    ON conversation_turns (handled_by_review_id) WHERE handled_by_review_id IS NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_conversation_branches_session
    ON conversation_branches (session_id) WHERE session_id IS NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_conversation_branches_parent
    ON conversation_branches (parent_branch_id) WHERE parent_branch_id IS NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_conversation_branches_fork_after
    ON conversation_branches (fork_after_turn_id) WHERE fork_after_turn_id IS NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_conversation_branches_replaced
    ON conversation_branches (replaced_turn_id) WHERE replaced_turn_id IS NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_conversation_branches_replacement
    ON conversation_branches (replacement_turn_id) WHERE replacement_turn_id IS NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_review_run_review
    ON review_run (review_id);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_shell_terminals_session
    ON shell_terminals (session_id);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_automation_runs_session
    ON automation_runs (session_id) WHERE session_id IS NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
-- The rest complete "every foreign key has an index on its child column"
-- (TestEveryForeignKeyHasAnIndexOnItsChildColumns): project deletes and agent
-- switch cleanup pay the same per-row scans.
CREATE INDEX IF NOT EXISTS idx_notifications_project
    ON notifications (project_id);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_review_project
    ON review (project_id);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_shell_terminals_project
    ON shell_terminals (project_id);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_usage_pr_measurement_project
    ON usage_pr_measurement (project_id);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_agent_switches_target_native_session
    ON agent_switches (target_native_session_ref) WHERE target_native_session_ref IS NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_agent_switch_failure_receipts_switch
    ON agent_switch_failure_receipts (switch_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_agent_switch_failure_receipts_switch;
DROP INDEX IF EXISTS idx_agent_switches_target_native_session;
DROP INDEX IF EXISTS idx_usage_pr_measurement_project;
DROP INDEX IF EXISTS idx_shell_terminals_project;
DROP INDEX IF EXISTS idx_review_project;
DROP INDEX IF EXISTS idx_notifications_project;
DROP INDEX IF EXISTS idx_automation_runs_session;
DROP INDEX IF EXISTS idx_shell_terminals_session;
DROP INDEX IF EXISTS idx_review_run_review;
DROP INDEX IF EXISTS idx_conversation_branches_replacement;
DROP INDEX IF EXISTS idx_conversation_branches_replaced;
DROP INDEX IF EXISTS idx_conversation_branches_fork_after;
DROP INDEX IF EXISTS idx_conversation_branches_parent;
DROP INDEX IF EXISTS idx_conversation_branches_session;
DROP INDEX IF EXISTS idx_conversation_turns_handled_by_review;
DROP INDEX IF EXISTS idx_conversation_turns_promoted_to;
DROP INDEX IF EXISTS idx_conversation_turns_retry_of;
DROP INDEX IF EXISTS idx_change_log_session;
-- +goose StatementEnd
