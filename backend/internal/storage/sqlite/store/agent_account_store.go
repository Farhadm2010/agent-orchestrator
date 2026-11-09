package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

var _ ports.AgentAccountStore = (*Store)(nil)

// ListAgentAccounts returns the saved accounts; the device accounts are implicit.
func (s *Store) ListAgentAccounts(ctx context.Context) ([]domain.AgentAccount, error) {
	rows, err := s.qr.ListAgentAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list agent accounts: %w", err)
	}
	out := make([]domain.AgentAccount, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.AgentAccount{
			ID: row.ID, Provider: domain.AgentAccountProvider(row.Provider), Home: row.Home,
			Email: row.Email, Plan: row.Plan, CreatedAt: row.CreatedAt,
		})
	}
	return out, nil
}

// CreateAgentAccount saves a signed-in account folder.
func (s *Store) CreateAgentAccount(ctx context.Context, a domain.AgentAccount) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.CreateAgentAccount(ctx, gen.CreateAgentAccountParams{
		ID: a.ID, Provider: string(a.Provider), Home: a.Home, Email: a.Email, Plan: a.Plan, CreatedAt: a.CreatedAt.UTC(),
	}); err != nil {
		return fmt.Errorf("create agent account %s: %w", a.ID, err)
	}
	return nil
}

// DeleteAgentAccount removes an account and any default that named it. Session
// bindings stay for usage history; a session bound to a removed account falls
// back to the provider's default at its next launch.
func (s *Store) DeleteAgentAccount(ctx context.Context, id string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "delete agent account", func(q *gen.Queries) error {
		if err := q.DeleteAgentAccountDefaultFor(ctx, id); err != nil {
			return err
		}
		return q.DeleteAgentAccount(ctx, id)
	})
}

// GetAgentAccountDefault returns the account new sessions of the provider use.
func (s *Store) GetAgentAccountDefault(ctx context.Context, p domain.AgentAccountProvider) (string, bool, error) {
	id, err := s.qr.GetAgentAccountDefault(ctx, string(p))
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read %s account default: %w", p, err)
	}
	return id, true, nil
}

// SetAgentAccountDefault makes accountID the provider's account for new sessions.
func (s *Store) SetAgentAccountDefault(ctx context.Context, p domain.AgentAccountProvider, accountID string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.SetAgentAccountDefault(ctx, gen.SetAgentAccountDefaultParams{Provider: string(p), AccountID: accountID}); err != nil {
		return fmt.Errorf("set %s account default: %w", p, err)
	}
	return nil
}

// BindSessionAgentAccount records the account a session uses from at on.
func (s *Store) BindSessionAgentAccount(ctx context.Context, id domain.SessionID, p domain.AgentAccountProvider, accountID string, at time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, fmt.Sprintf("bind session %s to %s account", id, p), func(q *gen.Queries) error {
		binding := gen.GetSessionAgentAccountParams{SessionID: string(id), Provider: string(p)}
		previous, previousErr := q.GetSessionAgentAccount(ctx, binding)
		if previousErr != nil && !errors.Is(previousErr, sql.ErrNoRows) {
			return previousErr
		}
		if err := q.BindSessionAgentAccount(ctx, gen.BindSessionAgentAccountParams{
			SessionID: string(id), Provider: string(p), AccountID: accountID, BoundAt: at.UTC(),
		}); err != nil {
			return err
		}
		if errors.Is(previousErr, sql.ErrNoRows) {
			return nil
		}
		current, err := q.GetSessionAgentAccount(ctx, binding)
		if err != nil {
			return err
		}
		if current == previous {
			return nil
		}
		return q.CancelSessionTurnAutoResume(ctx, gen.CancelSessionTurnAutoResumeParams{
			SessionID: id, Provider: string(p),
		})
	})
}

// GetSessionAgentAccount returns the account the session uses now for the provider.
func (s *Store) GetSessionAgentAccount(ctx context.Context, id domain.SessionID, p domain.AgentAccountProvider) (string, bool, error) {
	accountID, err := s.qr.GetSessionAgentAccount(ctx, gen.GetSessionAgentAccountParams{SessionID: string(id), Provider: string(p)})
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read session %s %s account: %w", id, p, err)
	}
	return accountID, true, nil
}

// AgentAccountUsageSince sums model usage per account, UTC day and model,
// including the spend of sessions deleted since. Callers pass the start of a
// UTC day, so the deleted sessions' day totals line up with the live events.
// An account, day and model may appear twice (live and deleted); callers sum.
func (s *Store) AgentAccountUsageSince(ctx context.Context, since time.Time) ([]domain.AgentAccountUsage, error) {
	since = since.UTC()
	rows, err := s.qr.AgentAccountUsageSince(ctx, gen.AgentAccountUsageSinceParams{Since: sql.NullTime{Time: since, Valid: true}})
	if err != nil {
		return nil, fmt.Errorf("read agent account usage: %w", err)
	}
	deleted, err := s.qr.DeletedSessionAccountUsageSince(ctx, since.Format(time.DateOnly))
	if err != nil {
		return nil, fmt.Errorf("read deleted sessions' agent account usage: %w", err)
	}
	out := make([]domain.AgentAccountUsage, 0, len(rows)+len(deleted))
	for _, row := range rows {
		out = append(out, domain.AgentAccountUsage{
			AccountID: row.AccountID, Day: row.Day, Model: row.ModelID,
			InputTokens: row.InputTokens, CachedInputTokens: row.CachedInputTokens, OutputTokens: row.OutputTokens,
			CostNanos: row.EstimatedCostNanos,
		})
	}
	for _, row := range deleted {
		out = append(out, domain.AgentAccountUsage{
			AccountID: row.AccountID, Day: row.Day, Model: row.ModelID,
			InputTokens: row.InputTokens, CachedInputTokens: row.CachedInputTokens, OutputTokens: row.OutputTokens,
			CostNanos: row.EstimatedCostNanos,
		})
	}
	return out, nil
}

// sessionAccountUsageBatchSQL is the account usage of session ?1's first ?2
// usage events in id order (-1 for all of them), attributed to accounts
// exactly as AgentAccountUsageSince does with since ?3. It starts from the
// session's bindings: AgentAccountUsageSince filtered by session scans every
// usage event in the database. Handwritten because sqlc cannot parse a
// grouped subquery; TestSessionDeleteKeepsAccountUsageInBatches holds the two
// to the same totals.
const sessionAccountUsageBatchSQL = `
SELECT u.account_id, u.day, u.model_id,
    CAST(COALESCE(SUM(u.input_tokens), 0) AS INTEGER),
    CAST(COALESCE(SUM(u.cached_input_tokens), 0) AS INTEGER),
    CAST(COALESCE(SUM(u.output_tokens), 0) AS INTEGER),
    CAST(COALESCE(SUM(u.estimated_cost_nanos), 0) AS INTEGER)
FROM (
    SELECT
        CAST(COALESCE(
            (SELECT a.account_id FROM session_agent_accounts a
             WHERE a.session_id = b.session_id
               AND a.provider = CASE b.harness WHEN 'claude-code' THEN 'claude' ELSE 'codex' END
               AND a.bound_at <= ev.created_at
             ORDER BY a.bound_at DESC LIMIT 1),
            (SELECT a.account_id FROM session_agent_accounts a
             WHERE a.session_id = b.session_id
               AND a.provider = CASE b.harness WHEN 'claude-code' THEN 'claude' ELSE 'codex' END
             ORDER BY a.bound_at ASC LIMIT 1),
            'device-' || CASE b.harness WHEN 'claude-code' THEN 'claude' ELSE 'codex' END
        ) AS TEXT) AS account_id,
        CAST(substr(ev.created_at, 1, 10) AS TEXT) AS day,
        ev.model_id, ev.input_tokens, ev.cached_input_tokens, ev.output_tokens,
        ev.estimated_cost_nanos, ev.created_at, b.harness
    FROM usage_bindings b
    JOIN model_usage_events ev ON ev.binding_id = b.id
    WHERE b.session_id = ?1
    ORDER BY ev.id
    LIMIT ?2
) u
WHERE u.created_at >= ?3 AND u.harness IN ('claude-code', 'codex')
GROUP BY 1, 2, 3`

// deleteSessionUsageBatchSQL deletes the events sessionAccountUsageBatchSQL
// just counted: session ?1's first ?2 usage events in id order.
const deleteSessionUsageBatchSQL = `
DELETE FROM model_usage_events WHERE id IN (
    SELECT ev.id FROM usage_bindings b
    JOIN model_usage_events ev ON ev.binding_id = b.id
    WHERE b.session_id = ?1
    ORDER BY ev.id
    LIMIT ?2
)`

// foldSessionAccountUsage adds the account usage of session id's first limit
// usage events (-1 for all) to the deleted-session totals, which keep a
// deleted session's spend in the agent account usage view. The caller deletes
// those events in the same transaction.
func foldSessionAccountUsage(ctx context.Context, tx *sql.Tx, id domain.SessionID, limit int) error {
	usage, err := sessionAccountUsageBatch(ctx, tx, id, limit)
	if err != nil {
		return err
	}
	q := gen.New(tx)
	for _, row := range usage {
		if err := q.AddDeletedSessionAccountUsage(ctx, row); err != nil {
			return err
		}
	}
	return nil
}

// sessionAccountUsageBatch reads what foldSessionAccountUsage adds. The rows
// are read to the end and closed before the fold writes on the same
// transaction.
func sessionAccountUsageBatch(ctx context.Context, tx *sql.Tx, id domain.SessionID, limit int) ([]gen.AddDeletedSessionAccountUsageParams, error) {
	rows, err := tx.QueryContext(ctx, sessionAccountUsageBatchSQL, string(id), limit, sql.NullTime{Time: time.Time{}, Valid: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var usage []gen.AddDeletedSessionAccountUsageParams
	for rows.Next() {
		var row gen.AddDeletedSessionAccountUsageParams
		if err := rows.Scan(&row.AccountID, &row.Day, &row.ModelID, &row.InputTokens,
			&row.CachedInputTokens, &row.OutputTokens, &row.EstimatedCostNanos); err != nil {
			return nil, err
		}
		usage = append(usage, row)
	}
	return usage, rows.Err()
}
