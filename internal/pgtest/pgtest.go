// Package pgtest gives each test package its own PostgreSQL schema.
//
// The integration tests drop and recreate their tables, and `go test ./...` runs packages
// in parallel: without a schema per package, the migrate tests and the store tests drop
// each other's tables and one of them fails for a reason that has nothing to do with the
// code. Isolating by schema is the fix rather than `-p 1`, because the isolation also
// holds when someone runs a single package, or two of them, on their own machine.
package pgtest

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	_ "github.com/lib/pq"
)

// Tables are dropped and recreated by tests, in dependency order.
const dropTables = `DROP TABLE IF EXISTS event_fires, events, config_revisions, audit_log, schema_migrations CASCADE`

// DSN returns a DSN whose search_path is a schema created for this test. It skips when
// LIVEOPS_TEST_DSN is unset, which is what keeps `go test ./...` useful on a laptop.
func DSN(t *testing.T) string {
	t.Helper()
	base := strings.TrimSpace(os.Getenv("LIVEOPS_TEST_DSN"))
	if base == "" {
		t.Skip("LIVEOPS_TEST_DSN is not set: run make test-db")
	}

	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatalf("random schema name: %v", err)
	}
	schema := "t_" + hex.EncodeToString(raw[:])

	admin, err := sql.Open("postgres", base)
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		admin.Close()
		t.Fatalf("create schema %s: %v", schema, err)
	}
	admin.Close()
	t.Cleanup(func() {
		// DROP is allowed even though the audit trigger refuses UPDATE, DELETE and
		// TRUNCATE: the trigger defends the data, not the schema. A deployment revokes
		// DROP from the application role, which is a step this repository names in
		// docs/design-notes.md rather than pretends to have taken.
		a, err := sql.Open("postgres", base)
		if err != nil {
			return
		}
		defer a.Close()
		_, _ = a.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
	})

	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + "search_path=" + schema
}

// Reset empties the schema so a test can start from nothing.
func Reset(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(dropTables); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
}
