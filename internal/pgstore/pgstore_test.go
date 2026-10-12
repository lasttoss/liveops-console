package pgstore

// These tests run against a real PostgreSQL, because everything this package promises
// is a database promise: a unique constraint, a primary key, a trigger, an advisory
// lock. A fake would only test the fake. `make test-db` starts the container; without
// LIVEOPS_TEST_DSN the file skips so that `go test ./...` stays useful on a laptop.
//
// Each test starts from dropped tables and re-runs the migrations, which also proves
// the migrations are the only thing that has to be true for the schema to exist. The
// audit table cannot be truncated on purpose (a trigger refuses), so DROP is how the
// fixture is reset; a deployment would revoke DROP from the application role.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/lasttoss/liveops-console/internal/auditlog"
	"github.com/lasttoss/liveops-console/internal/events"
	"github.com/lasttoss/liveops-console/internal/migrate"
	"github.com/lasttoss/liveops-console/internal/remoteconfig"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("LIVEOPS_TEST_DSN")
	if dsn == "" {
		t.Skip("LIVEOPS_TEST_DSN is not set: run make test-db")
	}
	st, err := Open(dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	ctx := context.Background()
	if _, err := st.DB().ExecContext(ctx, `DROP TABLE IF EXISTS event_fires, events, config_revisions, audit_log, schema_migrations CASCADE`); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if _, err := migrate.Apply(ctx, st.DB()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

func publish(t *testing.T, st *Store, key string, value string, expect *int) *remoteconfig.Revision {
	t.Helper()
	rev, err := st.Publish(context.Background(), remoteconfig.PublishInput{
		Game: "demo", Env: "dev", Key: key, Value: []byte(value), Actor: "em", ExpectVersion: expect,
	})
	if err != nil {
		t.Fatalf("publish %s: %v", value, err)
	}
	return rev
}

func TestPublishIncrementsVersions(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	v1 := publish(t, st, "feature.double_xp", `{"enabled":false}`, nil)
	if v1.Version != 1 {
		t.Fatalf("first publish must be version 1, got %d", v1.Version)
	}
	one := 1
	v2 := publish(t, st, "feature.double_xp", `{"enabled":true}`, &one)
	if v2.Version != 2 {
		t.Fatalf("second publish must be version 2, got %d", v2.Version)
	}
	cur, err := st.Current(ctx, "demo", "dev", "feature.double_xp")
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	if cur.Version != 2 {
		t.Fatalf("current must be the latest, got %d", cur.Version)
	}
}

// The whole reason for optimistic concurrency: the loser of a race is told which
// version won, and the value it tried to write is nowhere in the table.
func TestStaleExpectVersionIsAConflict(t *testing.T) {
	st := newTestStore(t)
	one := 1
	publish(t, st, "k", `{"v":1}`, nil)
	publish(t, st, "k", `{"v":2}`, &one) // wins, current is now 2

	_, err := st.Publish(context.Background(), remoteconfig.PublishInput{
		Game: "demo", Env: "dev", Key: "k", Value: []byte(`{"v":"loses"}`),
		Actor: "ci", ExpectVersion: &one,
	})
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("want a conflict, got %v", err)
	}
	if conflict.Current == nil || conflict.Current.Version != 2 {
		t.Fatalf("the conflict must carry the winning version, got %+v", conflict.Current)
	}
	if !errors.Is(err, remoteconfig.ErrConflict) {
		t.Fatal("the conflict must unwrap to ErrConflict so handlers can use errors.Is")
	}
	revs, err := st.Revisions(context.Background(), "demo", "dev", "k", 10)
	if err != nil {
		t.Fatalf("revisions: %v", err)
	}
	if len(revs) != 2 {
		t.Fatalf("a refused write must leave no trace in the table: want 2 revisions, got %d", len(revs))
	}
}

func TestCreateOnlyIfAbsent(t *testing.T) {
	st := newTestStore(t)
	zero := 0
	publish(t, st, "k", `{"v":1}`, &zero)
	_, err := st.Publish(context.Background(), remoteconfig.PublishInput{
		Game: "demo", Env: "dev", Key: "k", Value: []byte(`{"v":2}`), Actor: "ci", ExpectVersion: &zero,
	})
	if !errors.Is(err, remoteconfig.ErrConflict) {
		t.Fatalf("ExpectVersion 0 on an existing key must conflict, got %v", err)
	}
}

// A rollback is an append: the revision that was rolled back stays readable.
func TestRollbackCopiesTheOldPayloadForward(t *testing.T) {
	st := newTestStore(t)
	one := 1
	publish(t, st, "k", `{"enabled":false}`, nil)
	publish(t, st, "k", `{"enabled":true}`, &one)

	to := 1
	rev, err := st.Publish(context.Background(), remoteconfig.PublishInput{
		Game: "demo", Env: "dev", Key: "k", Actor: "em", Note: "bad release", FromVersion: &to,
	})
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if rev.Version != 3 {
		t.Fatalf("a rollback appends a new version, got %d", rev.Version)
	}
	sameJSON(t, rev.Value, `{"enabled":false}`)
	middle, err := st.Revision(context.Background(), "demo", "dev", "k", 2)
	if err != nil {
		t.Fatalf("revision 2 must still exist: %v", err)
	}
	sameJSON(t, middle.Value, `{"enabled":true}`)
	if _, err := st.Publish(context.Background(), remoteconfig.PublishInput{
		Game: "demo", Env: "dev", Key: "k", Actor: "em", FromVersion: func() *int { n := 99; return &n }(),
	}); !errors.Is(err, remoteconfig.ErrNotFound) {
		t.Fatalf("rolling back to a version that never existed must be a 404, got %v", err)
	}
}

func TestMissingKeyIsNotFoundNotAnError(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.Current(context.Background(), "demo", "dev", "nope"); !errors.Is(err, remoteconfig.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// "An audit trail that can be edited is a log." This asserts the trigger is real.
func TestAuditTrailCannotBeEdited(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if err := st.Audit(ctx, auditlog.Entry{Actor: "em", Action: "config.publish", Target: "config:demo/dev/k", Outcome: auditlog.OutcomeOK}); err != nil {
		t.Fatalf("audit insert: %v", err)
	}
	var id int64
	if err := st.DB().QueryRowContext(ctx, `SELECT id FROM audit_log ORDER BY id DESC LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("read id: %v", err)
	}

	if _, err := st.DB().ExecContext(ctx, `UPDATE audit_log SET actor = 'nobody' WHERE id = $1`, id); err == nil {
		t.Fatal("an UPDATE on the audit trail must fail")
	} else if !contains(err.Error(), "append-only") {
		t.Fatalf("the refusal should say why, got %v", err)
	}
	if _, err := st.DB().ExecContext(ctx, `DELETE FROM audit_log WHERE id = $1`, id); err == nil {
		t.Fatal("a DELETE on the audit trail must fail")
	}
	if _, err := st.DB().ExecContext(ctx, `TRUNCATE audit_log`); err == nil {
		t.Fatal("a TRUNCATE on the audit trail must fail")
	}
	// The row is still there, unchanged: the trail survived the attempt.
	entries, err := st.AuditPage(ctx, "config:demo/dev/k", 0, 10)
	if err != nil || len(entries) != 1 || entries[0].Actor != "em" {
		t.Fatalf("trail damaged: %v %+v", err, entries)
	}
}

// The flagship test. Four workers run at once over one due fire; exactly one effect is
// the only acceptable outcome, and it must hold when the workers are concurrent rather
// than because they happened to run in order.
func TestWorkerProducesOneEffectEvenUnderConcurrency(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	ev, err := st.CreateEvent(ctx, events.CreateInput{
		Game: "demo", Env: "dev", Name: "weekly_quest",
		StartsAt: now.Add(-2 * time.Minute), EndsAt: now.Add(-time.Minute),
		Payload: []byte(`{"reward":"100 coins"}`), Actor: "em",
	})
	if err != nil {
		t.Fatalf("create event: %v", err)
	}
	fires, err := st.Fires(ctx, ev.ID)
	if err != nil || len(fires) != 1 {
		t.Fatalf("want exactly one materialised fire, got %d (%v)", len(fires), err)
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		claimed int
	)
	start := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			<-start // release all four at the same instant
			got, err := st.ClaimDue(ctx, "worker-"+string(rune('a'+n)), 10)
			if err != nil {
				t.Errorf("claim: %v", err)
				return
			}
			mu.Lock()
			claimed += len(got)
			mu.Unlock()
		}(i)
	}
	close(start)
	wg.Wait()

	if claimed != 1 {
		t.Fatalf("exactly one worker must claim the fire, %d did", claimed)
	}
	snap, err := st.Stats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if snap.Effects != 1 {
		t.Fatalf("the effect must be produced once, count is %d", snap.Effects)
	}
	// Running again finds nothing: this is what "the worker is safe to restart" means.
	if again, err := st.ClaimDue(ctx, "worker-retry", 10); err != nil || len(again) != 0 {
		t.Fatalf("a second pass must claim nothing, got %d (%v)", len(again), err)
	}
	// And the trail says the same thing: one fire, in the same table as everything else.
	entries, err := st.AuditPage(ctx, "event:"+itoa(ev.ID), 0, 50)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	fires0 := 0
	for _, e := range entries {
		if e.Action == auditlog.ActionEventFire {
			fires0++
			sameJSON(t, e.After, `{"reward":"100 coins"}`)
		}
	}
	if fires0 != 1 {
		t.Fatalf("the trail must contain one fire, contains %d", fires0)
	}
}

func TestMigrationsAreIdempotent(t *testing.T) {
	st := newTestStore(t)
	ran, err := migrate.Apply(context.Background(), st.DB())
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if len(ran) != 0 {
		t.Fatalf("a second apply must run nothing, ran %v", ran)
	}
}

func TestEventScheduleIsMaterialisedOnCreate(t *testing.T) {
	st := newTestStore(t)
	now := time.Now().UTC()
	every := 60
	ev, err := st.CreateEvent(context.Background(), events.CreateInput{
		Game: "demo", Env: "dev", Name: "daily", StartsAt: now, EndsAt: now.Add(5 * time.Minute),
		RepeatEverySecs: &every, Actor: "em",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	fires, err := st.Fires(context.Background(), ev.ID)
	if err != nil {
		t.Fatalf("fires: %v", err)
	}
	if len(fires) != 6 {
		t.Fatalf("10:00 to 10:05 every minute is 6 fires, got %d", len(fires))
	}
	for _, f := range fires {
		if f.Status != "pending" || f.EffectCount != 0 {
			t.Fatalf("a fresh schedule must be pending with no effects: %+v", f)
		}
	}
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}

// sameJSON compares values, not bytes. jsonb normalises what it stores - key order
// and whitespace are the database's to choose - so a byte-for-byte assertion would
// test PostgreSQL's formatter rather than this repository's behaviour. The ETag is a
// version number and not a hash of the payload for the same reason.
func sameJSON(t *testing.T, got []byte, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("stored value is not JSON: %s (%v)", got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("bad expectation %q: %v", want, err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("value differs: got %s, want %s", got, want)
	}
}

// This test exists to make the normalisation above impossible to forget: the bytes you
// put into jsonb are not the bytes you get back.
func TestJSONBNormalisesWhatItStores(t *testing.T) {
	st := newTestStore(t)
	rev := publish(t, st, "norm", `{"b":1,"a":{"z":true,"y":[1,2]}}`, nil)
	if string(rev.Value) == `{"b":1,"a":{"z":true,"y":[1,2]}}` {
		t.Skip("this PostgreSQL build preserved the input verbatim, which the rest of this file does not assume")
	}
	sameJSON(t, rev.Value, `{"b":1,"a":{"z":true,"y":[1,2]}}`)
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
