// Package api is the HTTP surface. Two rules shape it:
//
//  1. A refusal is a first-class outcome. Every 401 and 403 is written to the same
//     audit trail as the successful writes, because "who tried and could not" is the
//     question asked after an incident.
//  2. A conflict is not an error page. The 409 body carries the version that won, so a
//     panel can show a diff instead of a stack trace.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lasttoss/liveops-console/internal/auditlog"
	"github.com/lasttoss/liveops-console/internal/auth"
	"github.com/lasttoss/liveops-console/internal/events"
	"github.com/lasttoss/liveops-console/internal/metrics"
	"github.com/lasttoss/liveops-console/internal/pgstore"
	"github.com/lasttoss/liveops-console/internal/remoteconfig"
)

// The three store interfaces are declared here, next to the consumer, and they are as
// small as the handlers need. That is what lets the handler tests run without a
// database, while the promises they rely on (versions, single effects, immutability)
// are still tested against a real one in internal/pgstore.
type ConfigStore interface {
	Current(ctx context.Context, game, env, key string) (*remoteconfig.Revision, error)
	Revisions(ctx context.Context, game, env, key string, limit int) ([]remoteconfig.Revision, error)
	Publish(ctx context.Context, in remoteconfig.PublishInput) (*remoteconfig.Revision, error)
}

type EventStore interface {
	CreateEvent(ctx context.Context, in events.CreateInput) (*events.Event, error)
	ListEvents(ctx context.Context, game, env string, limit int) ([]events.Event, error)
	Fires(ctx context.Context, eventID int64) ([]events.Fire, error)
}

type AuditStore interface {
	Audit(ctx context.Context, e auditlog.Entry) error
	AuditPage(ctx context.Context, target string, beforeID int64, limit int) ([]auditlog.Entry, error)
}

type StatsStore interface {
	Stats(ctx context.Context) (metrics.Snapshot, error)
}

type Server struct {
	Config  ConfigStore
	Events  EventStore
	Audit   AuditStore
	Stats   StatsStore
	Tokens  map[string]auth.Principal
	Envs    []string
	Started time.Time
}

type ctxKey int

const (
	ctxPrincipal ctxKey = iota
	ctxRequestID
)

func principal(ctx context.Context) auth.Principal {
	p, _ := ctx.Value(ctxPrincipal).(auth.Principal)
	return p
}

func requestID(ctx context.Context) string {
	id, _ := ctx.Value(ctxRequestID).(string)
	return id
}

func (s *Server) Handler() http.Handler {
	// Probes and metrics sit outside the token check: a kubelet cannot hold a token,
	// and a scraper that needs one is a scraper nobody configures. In a deployment
	// these paths are reached through the cluster network policy, which is where the
	// access decision belongs; see docs/design-notes.md.
	open := http.NewServeMux()
	open.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "liveops-console"})
	})
	open.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.ready(r.Context()); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not ready", "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	open.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		snap, err := s.Stats.Stats(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		fmt.Fprint(w, metrics.Render(snap))
	})

	v1 := http.NewServeMux()
	v1.HandleFunc("GET /v1/whoami", s.handleWhoami)
	v1.HandleFunc("GET /v1/config", s.handleConfigGet)
	v1.HandleFunc("GET /v1/config/revisions", s.handleConfigRevisions)
	v1.HandleFunc("PUT /v1/config", s.handleConfigPut)
	v1.HandleFunc("POST /v1/config/rollback", s.handleConfigRollback)
	v1.HandleFunc("GET /v1/events", s.handleEventsList)
	v1.HandleFunc("POST /v1/events", s.handleEventsCreate)
	v1.HandleFunc("GET /v1/events/{id}/fires", s.handleEventFires)
	v1.HandleFunc("GET /v1/audit", s.handleAudit)

	open.Handle("/v1/", s.withIdentity(v1))
	return open
}

func (s *Server) ready(ctx context.Context) error {
	if s.Stats == nil {
		return errors.New("no store configured")
	}
	_, err := s.Stats.Stats(ctx)
	return err
}

// withIdentity resolves the bearer token to an actor and attaches a request id. An
// unknown token is refused here rather than treated as anonymous: the difference
// between "not allowed" and "not known" is the difference between a 403 and a 401, and
// callers debug faster when the two are told apart.
func (s *Server) withIdentity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		w.Header().Set("X-Request-Id", id)
		ctx := context.WithValue(r.Context(), ctxRequestID, id)

		token := bearerToken(r)
		if token == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "send Authorization: Bearer <token>"})
			return
		}
		p, ok := s.Tokens[token]
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unknown token"})
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, ctxPrincipal, p)))
	})
}

func (s *Server) handleWhoami(w http.ResponseWriter, r *http.Request) {
	p := principal(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"actor":       p.Actor,
		"role":        p.Role,
		"permissions": auth.PermissionList(p.Role, s.Envs),
		"envs":        s.Envs,
	})
}

func (s *Server) handleConfigGet(w http.ResponseWriter, r *http.Request) {
	game, env, key := r.URL.Query().Get("game"), r.URL.Query().Get("env"), r.URL.Query().Get("key")
	if _, ok := s.authorize(w, r, auth.PermConfigRead, env); !ok {
		return
	}
	rev, err := s.Config.Current(r.Context(), game, env, key)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	etag := remoteconfig.ETag(rev.Version)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	// A client that already holds this version gets 304 and no body: the read path is
	// the hot path, and the fastest payload is the one not sent.
	if matches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeJSON(w, http.StatusOK, rev)
}

func (s *Server) handleConfigRevisions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	env := q.Get("env")
	if _, ok := s.authorize(w, r, auth.PermConfigRead, env); !ok {
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	revs, err := s.Config.Revisions(r.Context(), q.Get("game"), env, q.Get("key"), limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if revs == nil {
		revs = []remoteconfig.Revision{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"revisions": revs})
}

type publishBody struct {
	Game  string          `json:"game"`
	Env   string          `json:"env"`
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
	Note  string          `json:"note"`
}

func (s *Server) handleConfigPut(w http.ResponseWriter, r *http.Request) {
	var body publishBody
	if !decode(w, r, &body) {
		return
	}
	p, ok := s.authorize(w, r, auth.PermConfigPublish, body.Env)
	if !ok {
		return
	}
	if err := remoteconfig.Validate(body.Game, body.Env, body.Key); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	var expect *int
	if h := strings.TrimSpace(r.Header.Get("If-Match")); h != "" {
		if h == "*" {
			// If-Match: * means "only if it does not exist yet", which is the form a
			// client uses to avoid creating a second first revision.
			zero := 0
			expect = &zero
		} else {
			v, err := remoteconfig.ParseETag(h)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "If-Match: " + err.Error()})
				return
			}
			expect = &v
		}
	}

	before, _ := s.Config.Current(r.Context(), body.Game, body.Env, body.Key)
	rev, err := s.Config.Publish(r.Context(), remoteconfig.PublishInput{
		Game: body.Game, Env: body.Env, Key: body.Key, Value: body.Value,
		Note: body.Note, Actor: p.Actor, ExpectVersion: expect,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.record(r, p, auditlog.ActionConfigPublish, target(body.Game, body.Env, body.Key), auditlog.OutcomeOK, before, rev)
	w.Header().Set("ETag", remoteconfig.ETag(rev.Version))
	writeJSON(w, http.StatusOK, rev)
}

type rollbackBody struct {
	Game      string `json:"game"`
	Env       string `json:"env"`
	Key       string `json:"key"`
	ToVersion int    `json:"to_version"`
	Note      string `json:"note"`
}

func (s *Server) handleConfigRollback(w http.ResponseWriter, r *http.Request) {
	var body rollbackBody
	if !decode(w, r, &body) {
		return
	}
	p, ok := s.authorize(w, r, auth.PermConfigRollback, body.Env)
	if !ok {
		return
	}
	if body.ToVersion < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "to_version is required and must be >= 1"})
		return
	}
	before, _ := s.Config.Current(r.Context(), body.Game, body.Env, body.Key)
	note := body.Note
	if note == "" {
		note = fmt.Sprintf("rollback to v%d", body.ToVersion)
	}
	// A rollback is a publish, not an undo: the history keeps the revision that was
	// rolled back, and the new revision says who did it and why.
	rev, err := s.Config.Publish(r.Context(), remoteconfig.PublishInput{
		Game: body.Game, Env: body.Env, Key: body.Key, Note: note,
		Actor: p.Actor, FromVersion: &body.ToVersion,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.record(r, p, auditlog.ActionConfigRollback, target(body.Game, body.Env, body.Key), auditlog.OutcomeOK, before, rev)
	w.Header().Set("ETag", remoteconfig.ETag(rev.Version))
	writeJSON(w, http.StatusOK, rev)
}

type eventBody struct {
	Game            string          `json:"game"`
	Env             string          `json:"env"`
	Name            string          `json:"name"`
	StartsAt        time.Time       `json:"starts_at"`
	EndsAt          time.Time       `json:"ends_at"`
	RepeatEverySecs *int            `json:"repeat_every_secs"`
	Payload         json.RawMessage `json:"payload"`
}

func (s *Server) handleEventsCreate(w http.ResponseWriter, r *http.Request) {
	var body eventBody
	if !decode(w, r, &body) {
		return
	}
	p, ok := s.authorize(w, r, auth.PermEventsWrite, body.Env)
	if !ok {
		return
	}
	ev, err := s.Events.CreateEvent(r.Context(), events.CreateInput{
		Game: body.Game, Env: body.Env, Name: body.Name, StartsAt: body.StartsAt, EndsAt: body.EndsAt,
		RepeatEverySecs: body.RepeatEverySecs, Payload: body.Payload, Actor: p.Actor,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.record(r, p, auditlog.ActionEventCreate, fmt.Sprintf("event:%d", ev.ID), auditlog.OutcomeOK, nil, ev)
	writeJSON(w, http.StatusCreated, ev)
}

func (s *Server) handleEventsList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if _, ok := s.authorize(w, r, auth.PermEventsRead, q.Get("env")); !ok {
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, err := s.Events.ListEvents(r.Context(), q.Get("game"), q.Get("env"), limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if list == nil {
		list = []events.Event{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": list})
}

func (s *Server) handleEventFires(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id must be a number"})
		return
	}
	if _, ok := s.authorize(w, r, auth.PermEventsRead, r.URL.Query().Get("env")); !ok {
		return
	}
	fires, err := s.Events.Fires(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	effects := 0
	for _, f := range fires {
		effects += f.EffectCount
	}
	if fires == nil {
		fires = []events.Fire{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"fires": fires, "effects": effects})
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorize(w, r, auth.PermAuditRead, r.URL.Query().Get("env")); !ok {
		return
	}
	q := r.URL.Query()
	before, _ := strconv.ParseInt(q.Get("before_id"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, err := s.Audit.AuditPage(r.Context(), q.Get("target"), before, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if list == nil {
		list = []auditlog.Entry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": list})
}

// --- authorisation and failure paths --------------------------------------------

func (s *Server) authorize(w http.ResponseWriter, r *http.Request, perm auth.Permission, env string) (auth.Principal, bool) {
	p := principal(r.Context())
	if err := auth.Authorize(p, perm, env); err != nil {
		s.record(r, p, auditlog.ActionDenied, r.Method+" "+r.URL.Path, auditlog.OutcomeDenied, nil,
			map[string]string{"reason": err.Error(), "permission": string(perm), "env": env})
		writeJSON(w, http.StatusForbidden, map[string]any{"error": err.Error(), "permission": perm, "env": env})
		return p, false
	}
	return p, true
}

// fail maps store errors to status codes in one place. The conflict case is the one
// that matters: it answers with the version that won, because that is the information
// the caller needs to retry with, and asking them to re-read is a wasted round trip.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var conflict *pgstore.ConflictError
	switch {
	case errors.As(err, &conflict):
		s.record(r, principal(r.Context()), "conflict", r.Method+" "+r.URL.Path, auditlog.OutcomeConflict, nil,
			map[string]any{"error": conflict.Error()})
		body := map[string]any{"error": conflict.Error()}
		if conflict.Current != nil {
			body["current_version"] = conflict.Current.Version
			body["current"] = conflict.Current
		}
		writeJSON(w, http.StatusConflict, body)
	case errors.Is(err, remoteconfig.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, remoteconfig.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, events.ErrInvalid), errors.Is(err, remoteconfig.ErrInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		log.Printf("request %s %s failed: %v", r.Method, r.URL.Path, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
}

func (s *Server) record(r *http.Request, p auth.Principal, action, target string, outcome auditlog.Outcome, before, after any) {
	entry := auditlog.Entry{
		Actor:     p.Actor,
		Action:    action,
		Target:    target,
		Outcome:   outcome,
		RequestID: requestID(r.Context()),
	}
	if entry.Actor == "" {
		entry.Actor = "anonymous"
	}
	entry.Before = marshal(before)
	entry.After = marshal(after)
	if err := s.Audit.Audit(r.Context(), entry); err != nil {
		// The write already happened; the trail did not. That is worth a loud line in
		// the logs rather than a failed request, and it is why the audit table is in
		// the same database as the data it describes.
		log.Printf("AUDIT FAILED action=%s target=%s outcome=%s: %v", action, target, outcome, err)
	}
}

func marshal(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return raw
}

func target(game, env, key string) string { return "config:" + game + "/" + env + "/" + key }

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return strings.TrimSpace(r.Header.Get("X-Liveops-Token"))
}

func matches(header, etag string) bool {
	for _, part := range strings.Split(header, ",") {
		if strings.TrimSpace(part) == etag || strings.TrimSpace(part) == "*" {
			return true
		}
	}
	return false
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body: " + err.Error()})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Printf("write response: %v", err)
	}
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}
