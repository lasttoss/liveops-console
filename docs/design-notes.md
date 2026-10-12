# Design notes

The decisions that shaped this repository, and the alternatives that were rejected. Each
one is written as "what I chose / what I did not / why", because the second part is the
part an interviewer actually asks about.

## Configuration is a log, not a row

**Chosen:** every publish is an `INSERT` with a monotonically increasing `version`, and a
rollback copies an old payload into a new revision. Nothing is ever updated in place.

**Not chosen:** an `UPDATE` on a single row plus a separate history table. It is fewer
rows, and it makes the history table the thing that must not fail - so a crash between
the two writes produces a state where the answer to "what is live?" and "what does the
history say?" disagree.

A version is not a counter kept in Go. It is derived inside the transaction that writes
it, with `pg_advisory_xact_lock(hashtext(game|env|key))` taken first. That is why a
conflict is a definite answer rather than a retry loop: two writers to the same key are
serialised, the loser is told the exact version that won, and no update is lost.

## The `ETag` is a version, not a hash

**Chosen:** `ETag: "v17"`.

**Not chosen:** a hash of the payload. `jsonb` normalises what it stores - key order and
whitespace are the database's to choose - so a hash of the stored bytes would change the
`ETag` of a value that nobody edited. The integration tests compare values rather than
bytes for the same reason, and one test exists purely to record the normalisation.

## `409` carries the winner

**Chosen:** a stale `If-Match` returns `409` with `current_version` and the current
revision in the body.

**Not chosen:** `412 Precondition Failed`, which is the textbook answer for a failed
`If-Match`. It is defensible and I would accept it in someone else's code, but a panel
that receives `409` plus the winning revision can render a diff immediately, while `412`
tends to be handled as a generic "someone else changed it" toast.

## The effect and the claim are the same `UPDATE`

**Chosen:** the worker claims due fires with `UPDATE ... FROM (SELECT ... FOR UPDATE SKIP
LOCKED)`, incrementing `effect_count` in the same statement that flips the row to `done`,
and writes the audit row in the same transaction.

**Not chosen:** a `pending -> processing -> done` state machine with a lease. That design
exists to survive a worker dying mid-flight, which is a real problem - but it needs
expiry, takeover and idempotency keys handed to the *effect* itself to be correct, and
this repository's effect (recording that a reward was applied) does not need that. The
honest version of the trade: this design is exactly-once *per fire row*, and if the
effect were an HTTP call to another system, the other system would need its own
idempotency key. That is written down here rather than hidden in a comment.

## `FOR UPDATE SKIP LOCKED`, not a queue

**Chosen:** the database is the queue. `FOR UPDATE SKIP LOCKED` lets four workers run
concurrently without coordination and without a lock convoy.

**Not chosen:** Kafka/Redis/SQS. A queue would add a second source of truth for what
fired, and this repository's claim is that the *primary key* is what makes an effect
happen once. An external queue moves that guarantee into a component the reader cannot
see. The integration test spawns four workers at the same instant for the same reason.

## The audit trail is defended by a trigger

**Chosen:** a `BEFORE UPDATE OR DELETE` (and `BEFORE TRUNCATE`) trigger on `audit_log`
raises an exception, and a test asserts the exception is real.

**Not chosen (yet):** `REVOKE UPDATE, DELETE` from the application role, which is the
stronger answer because it survives someone with the trigger disabled. Note the
limitation plainly: **`DROP TABLE` is still allowed**, which means the same role that
writes the trail can delete it wholesale. In this project the application role owns the
schema because the migrations must run; a larger deployment separates the migrating role
from the running one and revokes `DROP` from the latter. That is the next step, and it is
not implemented here - saying so is cheaper than implying otherwise.

## Roles in code, tokens in the environment

**Chosen:** roles and permissions are a function in Go (`internal/auth`), the actors come
from `<token>:<actor>:<role>` entries in `LIVEOPS_TOKENS`, and production *writes* demand
`admin` while production *reads* do not.

**Not chosen:** an identity provider, a `users` table, OIDC. It would triple the size of
the repository and teach the reader nothing about authorisation. The two decisions worth
defending are that the actor is separate from the token (so rotating a token does not
rewrite history) and that the environment rule guards writes only (a rule that also
blocked reads would be worked around within a week).

Denials are recorded in the same trail as successes, with the reason. "Who tried and
could not" is the question asked after an incident, and answering it from a second system
is how the answer goes missing.

## Probes and metrics are unauthenticated

**Chosen:** `/healthz`, `/readyz` and `/metrics` answer without a token.

**Not chosen:** a token on `/metrics`. A kubelet cannot hold one, and a scraper that
needs a token is a scraper nobody configures. The access decision belongs to the network
policy in front of the service, which is what the Helm chart in `charts/liveops-console`
is for. This is a deliberate trade, not an oversight, and it is the reason the chart has a
`NetworkPolicy` enabled by default rather than as an option.

## The panel is a client, in TypeScript

**Chosen:** the UI is a separate Vite + React + TypeScript app under `panel/` that talks
to the same public API as the demo client, and it uses `If-Match` for edits so that two
people editing one key is a visible conflict rather than a lost change.

**Not chosen:** server-rendered HTML from Go templates. It would be fewer moving parts,
and it would also mean this repository still has zero lines of frontend code - which is
the gap it exists to close.

## What this repository does not claim

- Not multi-tenant: one database, one deployment, no per-tenant isolation story.
- Not a client SDK: `cmd/demo` is a Go HTTP client, not something a game engine links.
- Not billing, payments or inventory. The audit trail and the idempotent effect are the
  parts worth reading; the reward is a string in a JSON payload.
- Coverage is measured and printed, not targeted. The number that matters is which
  promises are asserted (versions, conflicts, one effect, an uneditable trail) rather
  than a percentage.
