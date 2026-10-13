# Measurements

Two coverage numbers are quoted in the README and they differ for a reason. Both are reproduced here,
because a reader who runs the wrong one would otherwise think the repository is lying.

## Environment

| | |
|---|---|
| Machine | 12-core x86-64 (AMD64), Ubuntu 24.04, Docker 29.1.3 |
| Go | 1.27.2 |
| Date | 2026-10-13 (UTC) |

Every host port in this file is the one the repository publishes: bound to loopback and offset from
the default, so a machine already running a Postgres, a Redis or three other stacks does not have to
stop them first.


## 55.7% statement coverage against a real PostgreSQL

```
$ make test-db          # starts a throwaway PostgreSQL, runs the suite against it, stops it
waiting for liveops-test-db
ok  .../cmd/console        1.071s  coverage: 30.7% of statements
ok  .../cmd/demo           1.009s  coverage:  6.7% of statements
ok  .../internal/api       1.039s  coverage: 65.9% of statements
ok  .../internal/auth      1.022s  coverage: 76.7% of statements
ok  .../internal/events    1.022s  coverage: 90.9% of statements
ok  .../internal/metrics   1.023s  coverage: 100.0% of statements
ok  .../internal/migrate   1.135s  coverage: 73.9% of statements
ok  .../internal/pgstore   1.666s  coverage: 67.4% of statements
ok  .../internal/remoteconfig 1.013s coverage: 100.0% of statements
total:                     (statements)  55.7%
test database stopped
```

## 34.6% without one

```
$ make coverage
total:                     (statements)  34.6%
```

**Result.** Both figures are exact. `internal/pgstore` goes from 0% to 67.4% and `internal/migrate`
from 17.4% to 73.9% when a database is present; what the store's tests cannot check without one is
written into the README rather than left out of the count.
