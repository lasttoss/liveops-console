-- Config is versioned, never updated in place. A publish is an insert; a rollback is
-- another insert carrying an older payload. Nothing in this table is ever mutated,
-- which is what makes the audit trail below able to reconstruct any past state.
CREATE TABLE IF NOT EXISTS config_revisions (
  id         bigserial PRIMARY KEY,
  game       text        NOT NULL,
  env        text        NOT NULL,
  key        text        NOT NULL,
  value      jsonb       NOT NULL,
  version    integer     NOT NULL,
  note       text        NOT NULL DEFAULT '',
  created_by text        NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (game, env, key, version)
);
CREATE INDEX IF NOT EXISTS config_revisions_lookup
  ON config_revisions (game, env, key, version DESC);

CREATE TABLE IF NOT EXISTS events (
  id                bigserial PRIMARY KEY,
  game              text        NOT NULL,
  env               text        NOT NULL,
  name              text        NOT NULL,
  starts_at         timestamptz NOT NULL,
  ends_at           timestamptz NOT NULL,
  repeat_every_secs integer,
  payload           jsonb       NOT NULL DEFAULT '{}'::jsonb,
  state             text        NOT NULL DEFAULT 'scheduled',
  created_by        text        NOT NULL,
  created_at        timestamptz NOT NULL DEFAULT now(),
  CHECK (ends_at > starts_at),
  -- A repeat shorter than a minute is a retry storm, not a schedule.
  CHECK (repeat_every_secs IS NULL OR repeat_every_secs >= 60)
);

-- One row per thing that must happen exactly once. The primary key is the guarantee:
-- a second scheduler pass cannot insert a fire that already exists, so the effect
-- below can only be produced by a row that was pending at that instant.
CREATE TABLE IF NOT EXISTS event_fires (
  event_id     bigint      NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  fire_at      timestamptz NOT NULL,
  status       text        NOT NULL DEFAULT 'pending',
  attempts     integer     NOT NULL DEFAULT 0,
  effect_count integer     NOT NULL DEFAULT 0,
  claimed_at   timestamptz,
  claimed_by   text,
  finished_at  timestamptz,
  PRIMARY KEY (event_id, fire_at)
);
CREATE INDEX IF NOT EXISTS event_fires_due
  ON event_fires (fire_at) WHERE status = 'pending';

CREATE TABLE IF NOT EXISTS audit_log (
  id         bigserial PRIMARY KEY,
  at         timestamptz NOT NULL DEFAULT now(),
  actor      text        NOT NULL,
  action     text        NOT NULL,
  target     text        NOT NULL,
  outcome    text        NOT NULL,
  before     jsonb,
  after      jsonb,
  request_id text        NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS audit_log_target ON audit_log (target, id DESC);
