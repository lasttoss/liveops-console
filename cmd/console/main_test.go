package main

// The two lines this worker prints are not decoration: the CI job greps for "claimed 1
// fire" and "claimed 0 fires" to prove that a second pass is a no-op. A string that a
// gate depends on deserves a test, otherwise a reworded message silently turns the gate
// into a check that always passes.

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lasttoss/liveops-console/internal/events"
	"github.com/lasttoss/liveops-console/internal/migrate"
	"github.com/lasttoss/liveops-console/internal/pgstore"
	"github.com/lasttoss/liveops-console/internal/pgtest"
)

func TestEnvHelpers(t *testing.T) {
	t.Setenv("LIVEOPS_X", "")
	if got := envOr("LIVEOPS_X", "fallback"); got != "fallback" {
		t.Fatalf("empty must fall back, got %q", got)
	}
	t.Setenv("LIVEOPS_X", "set")
	if got := envOr("LIVEOPS_X", "fallback"); got != "set" {
		t.Fatalf("set must win, got %q", got)
	}
	for _, c := range []struct {
		val  string
		want bool
	}{{"true", true}, {"1", true}, {"on", true}, {"YES", true}, {"false", false}, {"0", false}, {"off", false}, {"", true}} {
		t.Setenv("LIVEOPS_FLAG", c.val)
		if got := envBool("LIVEOPS_FLAG", true); got != c.want {
			t.Errorf("envBool(%q) = %v, want %v", c.val, got, c.want)
		}
	}
	if got := splitList(" dev , staging ,,prod "); len(got) != 3 || got[0] != "dev" || got[2] != "prod" {
		t.Fatalf("splitList wrong: %#v", got)
	}
}

func TestWorkerOncePrintsTheContractLines(t *testing.T) {
	ctx := context.Background()
	store, err := pgstore.Open(pgtest.DSN(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	pgtest.Reset(t, store.DB())
	if _, err := migrate.Apply(ctx, store.DB()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	now := time.Now().UTC()
	if _, err := store.CreateEvent(ctx, events.CreateInput{
		Game: "demo", Env: "dev", Name: "due", StartsAt: now.Add(-2 * time.Minute), EndsAt: now.Add(-time.Minute),
		Payload: []byte(`{"reward":"1 coin"}`), Actor: "test",
	}); err != nil {
		t.Fatalf("create event: %v", err)
	}

	first := captureStdout(t, func() {
		if err := runWorker(ctx, store, true, 0); err != nil {
			t.Fatalf("worker: %v", err)
		}
	})
	if !strings.Contains(first, "claimed 1 fire") {
		t.Fatalf("the first pass must report the claim: %q", first)
	}
	second := captureStdout(t, func() {
		if err := runWorker(ctx, store, true, 0); err != nil {
			t.Fatalf("worker: %v", err)
		}
	})
	if !strings.Contains(second, "claimed 0 fires") {
		t.Fatalf("the second pass must report nothing pending: %q", second)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	saved := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		raw, _ := io.ReadAll(r)
		done <- string(raw)
	}()
	fn()
	w.Close()
	os.Stdout = saved
	return <-done
}
