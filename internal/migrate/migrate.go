// Package migrate applies the SQL files embedded in the binary. Embedded rather than
// read from disk because the runtime image is distroless: it has no shell and no
// checkout, so a migration that lives outside the binary cannot run in production.
package migrate

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
)

//go:embed migrations/*.sql
var files embed.FS

const lockKey = 918273645 // one migrator at a time, even during a rolling deploy

// Apply runs every migration that has not been recorded yet, in filename order.
func Apply(ctx context.Context, db *sql.DB) ([]string, error) {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			name       text PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return nil, fmt.Errorf("create schema_migrations: %w", err)
	}

	names, err := available()
	if err != nil {
		return nil, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// An advisory lock taken inside the transaction is released on commit, so a
	// second instance starting at the same time waits instead of racing.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, lockKey); err != nil {
		return nil, fmt.Errorf("advisory lock: %w", err)
	}

	applied := map[string]bool{}
	rows, err := tx.QueryContext(ctx, `SELECT name FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, err
		}
		applied[n] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var ran []string
	for _, name := range names {
		if applied[name] {
			continue
		}
		body, err := files.ReadFile("migrations/" + name)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			return nil, fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (name) VALUES ($1)`, name); err != nil {
			return nil, err
		}
		ran = append(ran, name)
	}
	return ran, tx.Commit()
}

func available() ([]string, error) {
	entries, err := files.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}
