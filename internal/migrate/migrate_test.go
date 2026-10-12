package migrate

// The migrations are the schema, and the schema is where this repository's promises are
// enforced - so the test asserts the promises exist as objects, not that Apply returned
// without an error.

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/lib/pq"

	"github.com/lasttoss/liveops-console/internal/pgtest"
)

func TestAvailableListsEveryEmbeddedMigrationInOrder(t *testing.T) {
	names, err := available()
	if err != nil {
		t.Fatalf("available: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("want 2 migrations, got %v", names)
	}
	if names[0] >= names[1] {
		t.Fatalf("migrations must be ordered by name, got %v", names)
	}
}

func TestApplyCreatesTheSchemaAndTheGuarantees(t *testing.T) {
	db, err := sql.Open("postgres", pgtest.DSN(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	pgtest.Reset(t, db)
	ran, err := Apply(ctx, db)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(ran) != 2 {
		t.Fatalf("a fresh database should need both migrations, got %v", ran)
	}

	for _, table := range []string{"config_revisions", "events", "event_fires", "audit_log", "schema_migrations"} {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil {
			t.Fatalf("look up %s: %v", table, err)
		}
		if !exists {
			t.Errorf("table %s was not created", table)
		}
	}

	// The append-only trigger is the promise this migration exists for, so it is looked
	// up by name rather than assumed from the SQL file being present.
	var triggers int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM pg_trigger WHERE tgrelid = 'audit_log'::regclass AND NOT tgisinternal`,
	).Scan(&triggers); err != nil {
		t.Fatalf("count triggers: %v", err)
	}
	if triggers != 2 {
		t.Fatalf("want the row trigger and the truncate trigger, found %d", triggers)
	}

	// And the primary key that makes an effect happen once.
	var pk int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM pg_index i
		JOIN pg_class c ON c.oid = i.indrelid
		WHERE c.relname = 'event_fires' AND i.indisprimary`).Scan(&pk); err != nil {
		t.Fatalf("look up primary key: %v", err)
	}
	if pk != 1 {
		t.Fatalf("event_fires must have exactly one primary key, found %d", pk)
	}

	if again, err := Apply(ctx, db); err != nil || len(again) != 0 {
		t.Fatalf("a second apply must do nothing: ran %v (%v)", again, err)
	}
}
