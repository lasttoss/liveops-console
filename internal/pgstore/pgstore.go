// Package pgstore is the only package that speaks SQL. Everything it promises is
// enforced by the database rather than by discipline in the callers: a version number
// comes from a unique constraint, an effect happens once because a primary key says
// so, and the audit trail cannot be edited because a trigger refuses.
package pgstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "github.com/lib/pq"

	"github.com/lasttoss/liveops-console/internal/auditlog"
	"github.com/lasttoss/liveops-console/internal/events"
	"github.com/lasttoss/liveops-console/internal/metrics"
	"github.com/lasttoss/liveops-console/internal/remoteconfig"
)

type Store struct{ db *sql.DB }

func Open(dsn string) (*Store, error) {
	if dsn == "" {
		return nil, errors.New("empty DSN")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(8)
	db.SetConnMaxLifetime(30 * time.Minute)
	return &Store{db: db}, nil
}

func (s *Store) DB() *sql.DB                    { return s.db }
func (s *Store) Close() error                   { return s.db.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// ConflictError carries the version that actually won, so the caller can answer with
// it instead of guessing. It unwraps to remoteconfig.ErrConflict so a handler can use
// errors.Is and stay out of this package's business.
type ConflictError struct{ Current *remoteconfig.Revision }

func (e *ConflictError) Error() string {
	if e.Current == nil {
		return "version conflict: the key does not exist"
	}
	return fmt.Sprintf("version conflict: the current version is %d", e.Current.Version)
}

func (e *ConflictError) Unwrap() error { return remoteconfig.ErrConflict }

// --- configuration -------------------------------------------------------------

func keyLock(game, env, key string) string { return game + "|" + env + "|" + key }

func scanRevision(row interface{ Scan(...any) error }, game, env, key string) (*remoteconfig.Revision, error) {
	rev := &remoteconfig.Revision{Game: game, Env: env, Key: key}
	var value []byte
	if err := row.Scan(&rev.ID, &value, &rev.Version, &rev.Note, &rev.CreatedBy, &rev.CreatedAt); err != nil {
		return nil, err
	}
	rev.Value = json.RawMessage(value)
	return rev, nil
}

const revisionColumns = `id, value, version, note, created_by, created_at`

// noRowsToNotFound is the difference between a 404 and a 500. database/sql reports a
// missing row as an error, and a handler that lets it through tells the caller the
// server broke when in fact the key was simply never published.
func noRowsToNotFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return remoteconfig.ErrNotFound
	}
	return err
}

func (s *Store) Current(ctx context.Context, game, env, key string) (*remoteconfig.Revision, error) {
	rev, err := scanRevision(s.db.QueryRowContext(ctx, `
		SELECT `+revisionColumns+` FROM config_revisions
		WHERE game = $1 AND env = $2 AND key = $3
		ORDER BY version DESC LIMIT 1`, game, env, key), game, env, key)
	return rev, noRowsToNotFound(err)
}

func (s *Store) Revision(ctx context.Context, game, env, key string, version int) (*remoteconfig.Revision, error) {
	rev, err := scanRevision(s.db.QueryRowContext(ctx, `
		SELECT `+revisionColumns+` FROM config_revisions
		WHERE game = $1 AND env = $2 AND key = $3 AND version = $4`, game, env, key, version), game, env, key)
	return rev, noRowsToNotFound(err)
}

func (s *Store) Revisions(ctx context.Context, game, env, key string, limit int) ([]remoteconfig.Revision, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+revisionColumns+` FROM config_revisions
		WHERE game = $1 AND env = $2 AND key = $3
		ORDER BY version DESC LIMIT $4`, game, env, key, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []remoteconfig.Revision
	for rows.Next() {
		rev, err := scanRevision(rows, game, env, key)
		if err != nil {
			return nil, err
		}
		out = append(out, *rev)
	}
	return out, rows.Err()
}

// Publish appends a revision. Two writes to the same key are serialised by an advisory
// lock taken inside the transaction, so the version a caller sees in a conflict is the
// version that really won - no retry loop, no lost update.
func (s *Store) Publish(ctx context.Context, in remoteconfig.PublishInput) (*remoteconfig.Revision, error) {
	if err := remoteconfig.Validate(in.Game, in.Env, in.Key); err != nil {
		return nil, err
	}
	if in.Actor == "" {
		return nil, errors.New("actor is required: an unattributed change is not auditable")
	}
	if in.FromVersion == nil && len(in.Value) == 0 {
		return nil, errors.New("value is required unless from_version names a revision to copy")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, keyLock(in.Game, in.Env, in.Key)); err != nil {
		return nil, fmt.Errorf("lock key: %w", err)
	}

	current, err := scanRevision(tx.QueryRowContext(ctx, `
		SELECT `+revisionColumns+` FROM config_revisions
		WHERE game = $1 AND env = $2 AND key = $3
		ORDER BY version DESC LIMIT 1`, in.Game, in.Env, in.Key), in.Game, in.Env, in.Key)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		current = nil
	case err != nil:
		return nil, err
	}

	currentVersion := 0
	if current != nil {
		currentVersion = current.Version
	}
	if in.ExpectVersion != nil && *in.ExpectVersion != currentVersion {
		return nil, &ConflictError{Current: current}
	}

	value := in.Value
	if in.FromVersion != nil {
		if err := tx.QueryRowContext(ctx, `
			SELECT value FROM config_revisions
			WHERE game = $1 AND env = $2 AND key = $3 AND version = $4`,
			in.Game, in.Env, in.Key, *in.FromVersion).Scan(&value); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("revision %d of %s: %w", *in.FromVersion, keyLock(in.Game, in.Env, in.Key), remoteconfig.ErrNotFound)
			}
			return nil, err
		}
	}

	rev := &remoteconfig.Revision{
		Game: in.Game, Env: in.Env, Key: in.Key,
		Value: json.RawMessage(value), Version: currentVersion + 1,
		Note: in.Note, CreatedBy: in.Actor,
	}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO config_revisions (game, env, key, value, version, note, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at`,
		rev.Game, rev.Env, rev.Key, []byte(rev.Value), rev.Version, rev.Note, rev.CreatedBy,
	).Scan(&rev.ID, &rev.CreatedAt); err != nil {
		return nil, fmt.Errorf("insert revision: %w", err)
	}
	return rev, tx.Commit()
}

// --- events --------------------------------------------------------------------

const eventColumns = `id, game, env, name, starts_at, ends_at, repeat_every_secs, payload, state, created_by, created_at`

func scanEvent(row interface{ Scan(...any) error }) (*events.Event, error) {
	ev := &events.Event{}
	var repeat sql.NullInt64
	var payload []byte
	if err := row.Scan(&ev.ID, &ev.Game, &ev.Env, &ev.Name, &ev.StartsAt, &ev.EndsAt, &repeat, &payload, &ev.State, &ev.CreatedBy, &ev.CreatedAt); err != nil {
		return nil, err
	}
	ev.Payload = json.RawMessage(payload)
	if repeat.Valid {
		n := int(repeat.Int64)
		ev.RepeatEverySecs = &n
	}
	return ev, nil
}

// CreateEvent stores the schedule and, in the same transaction, the instants it must
// fire at. Materialising them up front is what lets a primary key - not a scheduler's
// memory - be the thing that stops a reward from being paid twice.
func (s *Store) CreateEvent(ctx context.Context, in events.CreateInput) (*events.Event, error) {
	if err := events.Validate(in); err != nil {
		return nil, err
	}
	if in.Actor == "" {
		return nil, errors.New("actor is required")
	}
	payload := in.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	ev := &events.Event{
		Game: in.Game, Env: in.Env, Name: in.Name, StartsAt: in.StartsAt, EndsAt: in.EndsAt,
		RepeatEverySecs: in.RepeatEverySecs, Payload: json.RawMessage(payload),
		State: "scheduled", CreatedBy: in.Actor,
	}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO events (game, env, name, starts_at, ends_at, repeat_every_secs, payload, state, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, created_at`,
		ev.Game, ev.Env, ev.Name, ev.StartsAt, ev.EndsAt, ev.RepeatEverySecs, []byte(ev.Payload), ev.State, ev.CreatedBy,
	).Scan(&ev.ID, &ev.CreatedAt); err != nil {
		return nil, fmt.Errorf("insert event: %w", err)
	}

	for _, fireAt := range events.Fires(in.StartsAt, in.EndsAt, in.Repeat(), events.MaxFires) {
		// ON CONFLICT DO NOTHING: creating the same event twice must not double the
		// schedule, and the primary key is the reason that is true.
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO event_fires (event_id, fire_at) VALUES ($1, $2)
			ON CONFLICT (event_id, fire_at) DO NOTHING`, ev.ID, fireAt); err != nil {
			return nil, fmt.Errorf("materialise fire at %s: %w", fireAt, err)
		}
	}
	return ev, tx.Commit()
}

func (s *Store) ListEvents(ctx context.Context, game, env string, limit int) ([]events.Event, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+eventColumns+` FROM events
		WHERE ($1 = '' OR game = $1) AND ($2 = '' OR env = $2)
		ORDER BY starts_at DESC LIMIT $3`, game, env, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []events.Event
	for rows.Next() {
		ev, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *ev)
	}
	return out, rows.Err()
}

func (s *Store) Event(ctx context.Context, id int64) (*events.Event, error) {
	return scanEvent(s.db.QueryRowContext(ctx, `SELECT `+eventColumns+` FROM events WHERE id = $1`, id))
}

func (s *Store) Fires(ctx context.Context, eventID int64) ([]events.Fire, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT event_id, fire_at, status, attempts, effect_count, claimed_at, claimed_by
		FROM event_fires WHERE event_id = $1 ORDER BY fire_at`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []events.Fire
	for rows.Next() {
		var f events.Fire
		if err := rows.Scan(&f.EventID, &f.FireAt, &f.Status, &f.Attempts, &f.EffectCount, &f.ClaimedAt, &f.ClaimedBy); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ClaimDue is the worker's whole job, in one statement and one transaction: take the
// due fires, mark them done, and record the effect. The claim and the effect are the
// same UPDATE, which is why running the worker twice cannot pay twice - the second run
// finds nothing pending. FOR UPDATE SKIP LOCKED is what makes concurrent workers safe
// rather than merely lucky, and the integration test runs four of them at once.
func (s *Store) ClaimDue(ctx context.Context, worker string, limit int) ([]events.Fire, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
		WITH due AS (
			SELECT event_id, fire_at FROM event_fires
			WHERE status = 'pending' AND fire_at <= now()
			ORDER BY fire_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE event_fires f
		SET status = 'done',
		    attempts = f.attempts + 1,
		    effect_count = f.effect_count + 1,
		    claimed_at = now(),
		    claimed_by = $2,
		    finished_at = now()
		FROM due
		JOIN events e ON e.id = due.event_id
		WHERE f.event_id = due.event_id AND f.fire_at = due.fire_at
		RETURNING f.event_id, f.fire_at, f.effect_count, e.game, e.env, e.name, e.payload`, limit, worker)
	if err != nil {
		return nil, err
	}

	type claimed struct {
		fire    events.Fire
		payload []byte
	}
	var taken []claimed
	for rows.Next() {
		var c claimed
		var game, env, name string
		if err := rows.Scan(&c.fire.EventID, &c.fire.FireAt, &c.fire.EffectCount, &game, &env, &name, &c.payload); err != nil {
			rows.Close()
			return nil, err
		}
		c.fire.Status = "done"
		taken = append(taken, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]events.Fire, 0, len(taken))
	for _, c := range taken {
		// The effect is recorded in the audit trail, in the same transaction as the
		// claim: an effect that is not in the trail did not happen.
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO audit_log (actor, action, target, outcome, after)
			VALUES ($1, $2, $3, $4, $5)`,
			worker, auditlog.ActionEventFire, fmt.Sprintf("event:%d", c.fire.EventID),
			string(auditlog.OutcomeOK), []byte(c.payload)); err != nil {
			return nil, fmt.Errorf("record effect: %w", err)
		}
		out = append(out, c.fire)
	}
	return out, tx.Commit()
}

// --- audit ---------------------------------------------------------------------

func (s *Store) Audit(ctx context.Context, e auditlog.Entry) error {
	if e.Outcome == "" {
		e.Outcome = auditlog.OutcomeOK
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO audit_log (actor, action, target, outcome, before, after, request_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		e.Actor, e.Action, e.Target, string(e.Outcome), nullable(e.Before), nullable(e.After), e.RequestID)
	return err
}

func nullable(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return []byte(raw)
}

const auditColumns = `id, at, actor, action, target, outcome, before, after, request_id`

func scanAudit(row interface{ Scan(...any) error }) (*auditlog.Entry, error) {
	e := &auditlog.Entry{}
	var outcome string
	var before, after []byte
	if err := row.Scan(&e.ID, &e.At, &e.Actor, &e.Action, &e.Target, &outcome, &before, &after, &e.RequestID); err != nil {
		return nil, err
	}
	e.Outcome = auditlog.Outcome(outcome)
	e.Before = json.RawMessage(before)
	e.After = json.RawMessage(after)
	return e, nil
}

// AuditPage walks the trail by keyset, not by OFFSET: an audit table only grows, and
// the page a reader is on must not shift because something was written underneath.
func (s *Store) AuditPage(ctx context.Context, target string, beforeID int64, limit int) ([]auditlog.Entry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+auditColumns+` FROM audit_log
		WHERE ($1 = '' OR target = $1) AND ($2 = 0 OR id < $2)
		ORDER BY id DESC LIMIT $3`, target, beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []auditlog.Entry
	for rows.Next() {
		e, err := scanAudit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// --- stats for /metrics ---------------------------------------------------------

func (s *Store) Stats(ctx context.Context) (metrics.Snapshot, error) {
	var st metrics.Snapshot
	err := s.db.QueryRowContext(ctx, `
		SELECT
			(SELECT count(*) FROM config_revisions),
			(SELECT count(DISTINCT (game, env, key)) FROM config_revisions),
			(SELECT count(*) FROM events),
			(SELECT count(*) FROM event_fires WHERE status = 'pending'),
			(SELECT coalesce(sum(effect_count), 0) FROM event_fires),
			(SELECT count(*) FROM audit_log),
			(SELECT count(*) FROM audit_log WHERE outcome = 'denied')`).
		Scan(&st.Revisions, &st.ConfigKeys, &st.Events, &st.PendingFires, &st.Effects, &st.AuditRows, &st.Denials)
	return st, err
}
