# Provenance

This repository is a clean-room implementation written after hours on a personal
machine. It exists because the work I do during the day - LiveOps back offices for live
games: remote configuration, scheduled events, audit trails, role-scoped writes - is
interesting and almost none of it is public.

What was carried over is the *problem*, not the code:

| Carried over | Not carried over |
|---|---|
| The problem shape: versioned configuration, scheduled rewards, an audit trail, writes scoped by role and environment | Any source file, query, table name, endpoint path or message string from an employer's codebase |
| The bugs worth designing against: a double-fired reward, an audit row that someone edited, a `409` that does not say which version won | Any real game name, tenant name, host, domain, bucket or credential |
| The environment rule that production writes need a stronger role than development writes | Any player data, log line, configuration value or identifier from a live deployment |

Everything here was written for this repository: the schema, the HTTP surface, the
tests, the tokens and the demo game (`game=demo`, `env=dev|staging|prod`). Third-party
software is used as a dependency (PostgreSQL, `lib/pq`) or credited where it inspired a
design (see docs/design-notes.md).
