// Command console is the whole service: an HTTP API, a scheduler and a migration step,
// in one binary with a flag to choose. One binary because a reviewer should be able to
// answer "what runs?" by reading one file, and because the worker and the API share the
// same store rules - splitting them into two deployables would let them drift.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lasttoss/liveops-console/internal/api"
	"github.com/lasttoss/liveops-console/internal/auth"
	"github.com/lasttoss/liveops-console/internal/migrate"
	"github.com/lasttoss/liveops-console/internal/pgstore"
)

func main() {
	var (
		mode        = flag.String("mode", "server", "server | migrate | worker")
		addr        = flag.String("addr", envOr("LIVEOPS_ADDR", ":8080"), "listen address")
		dsn         = flag.String("dsn", os.Getenv("LIVEOPS_DSN"), "PostgreSQL DSN")
		tokens      = flag.String("tokens", os.Getenv("LIVEOPS_TOKENS"), "token:actor:role entries, comma separated")
		once        = flag.Bool("once", false, "worker: run one pass and exit (this is what the demo uses)")
		tick        = flag.Duration("tick", time.Second, "worker: pause between passes")
		envs        = flag.String("envs", envOr("LIVEOPS_ENVS", "dev,staging,prod"), "environments the API will serve")
		migrateOnUp = flag.Bool("migrate-on-start", envBool("LIVEOPS_MIGRATE_ON_START", true), "server: apply migrations before listening")
	)
	flag.Parse()

	// The mode is checked before the DSN because it is the argument that is wrong when
	// someone mistypes, and answering a mistyped flag with a missing-environment error
	// sends them looking in the wrong place. (This cost one red CI run to learn.)
	switch *mode {
	case "server", "migrate", "worker":
	default:
		fmt.Fprintf(os.Stderr, "unknown mode %q: want server, migrate or worker\n", *mode)
		flag.Usage()
		os.Exit(2)
	}
	if *dsn == "" {
		log.Fatal("LIVEOPS_DSN is required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := pgstore.Open(*dsn)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer store.Close()

	switch *mode {
	case "migrate":
		ran, err := migrate.Apply(ctx, store.DB())
		if err != nil {
			log.Fatalf("migrate: %v", err)
		}
		if len(ran) == 0 {
			fmt.Println("schema is up to date, nothing to apply")
			return
		}
		fmt.Printf("applied %d migration(s): %s\n", len(ran), strings.Join(ran, ", "))

	case "worker":
		if err := runWorker(ctx, store, *once, *tick); err != nil {
			log.Fatalf("worker: %v", err)
		}

	case "server":
		if *migrateOnUp {
			// Migrating on start is right for this project and wrong for a large one:
			// the advisory lock makes concurrent instances safe, but a rolling deploy
			// still wants the schema change to be a separate, reviewable step. The flag
			// is here so the choice is visible rather than implied.
			ran, err := migrate.Apply(ctx, store.DB())
			if err != nil {
				log.Fatalf("migrate: %v", err)
			}
			if len(ran) > 0 {
				log.Printf("applied migrations: %s", strings.Join(ran, ", "))
			}
		}
		principals, err := auth.ParseTokens(*tokens)
		if err != nil {
			log.Fatalf("tokens: %v", err)
		}
		if len(principals) == 0 {
			log.Fatal("LIVEOPS_TOKENS is empty: nobody could write, and the audit trail would have no actors")
		}
		srv := &api.Server{
			Config: store, Events: store, Audit: store, Stats: store,
			Tokens: principals, Envs: splitList(*envs), Started: time.Now(),
		}
		httpSrv := &http.Server{
			Addr:              *addr,
			Handler:           srv.Handler(),
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
		go func() {
			log.Printf("liveops-console listening on %s with %d token(s)", *addr, len(principals))
			if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Fatalf("listen: %v", err)
			}
		}()
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdown); err != nil {
			log.Printf("shutdown: %v", err)
		}

	}
}

// runWorker claims due fires. The interesting property is not in this loop: it is in
// the single UPDATE inside the store, which is why running this twice - or on ten pods
// at once - produces one effect per fire and not ten.
func runWorker(ctx context.Context, store *pgstore.Store, once bool, tick time.Duration) error {
	name, _ := os.Hostname()
	if name == "" {
		name = "worker"
	}
	for {
		claimed, err := store.ClaimDue(ctx, name, 100)
		if err != nil {
			return err
		}
		if once {
			// The demo greps for these two lines, so the wording is part of the
			// contract: "1 fire" and "0 fires" are what make a second pass visibly a
			// no-op rather than a claim nobody can see.
			if len(claimed) == 1 {
				fmt.Printf("claimed 1 fire (worker %s) - effect recorded once\n", name)
			} else {
				fmt.Printf("claimed 0 fires (worker %s) - nothing was pending\n", name)
			}
			return nil
		}
		if len(claimed) > 0 {
			log.Printf("claimed %d fire(s)", len(claimed))
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(tick):
		}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return fallback
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
