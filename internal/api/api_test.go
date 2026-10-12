package api

// The handler tests run against fakes, and the fakes mirror the storage semantics
// rather than the other way round: versions increment, a stale expectation conflicts,
// a rollback copies forward. The real guarantees are tested against PostgreSQL in
// internal/pgstore; what is tested here is the HTTP layer - status codes, headers and
// the promise that a refusal is recorded in the same trail as a success.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lasttoss/liveops-console/internal/auditlog"
	"github.com/lasttoss/liveops-console/internal/auth"
	"github.com/lasttoss/liveops-console/internal/events"
	"github.com/lasttoss/liveops-console/internal/metrics"
	"github.com/lasttoss/liveops-console/internal/pgstore"
	"github.com/lasttoss/liveops-console/internal/remoteconfig"
)

type fakeConfig struct {
	mu   sync.Mutex
	revs map[string][]remoteconfig.Revision
}

func newFakeConfig() *fakeConfig { return &fakeConfig{revs: map[string][]remoteconfig.Revision{}} }

func kk(game, env, key string) string { return game + "/" + env + "/" + key }

func (f *fakeConfig) Current(_ context.Context, game, env, key string) (*remoteconfig.Revision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	list := f.revs[kk(game, env, key)]
	if len(list) == 0 {
		return nil, remoteconfig.ErrNotFound
	}
	last := list[len(list)-1]
	return &last, nil
}

func (f *fakeConfig) Revisions(_ context.Context, game, env, key string, _ int) ([]remoteconfig.Revision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.revs[kk(game, env, key)], nil
}

func (f *fakeConfig) Publish(_ context.Context, in remoteconfig.PublishInput) (*remoteconfig.Revision, error) {
	if err := remoteconfig.Validate(in.Game, in.Env, in.Key); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	k := kk(in.Game, in.Env, in.Key)
	list := f.revs[k]
	current := 0
	var cur *remoteconfig.Revision
	if len(list) > 0 {
		current = list[len(list)-1].Version
		cur = &list[len(list)-1]
	}
	if in.ExpectVersion != nil && *in.ExpectVersion != current {
		return nil, &pgstore.ConflictError{Current: cur}
	}
	value := in.Value
	if in.FromVersion != nil {
		found := false
		for _, r := range list {
			if r.Version == *in.FromVersion {
				value, found = r.Value, true
			}
		}
		if !found {
			return nil, remoteconfig.ErrNotFound
		}
	}
	rev := remoteconfig.Revision{
		Game: in.Game, Env: in.Env, Key: in.Key, Value: value, Version: current + 1,
		Note: in.Note, CreatedBy: in.Actor, CreatedAt: time.Now().UTC(),
	}
	f.revs[k] = append(list, rev)
	return &rev, nil
}

type fakeEvents struct {
	mu      sync.Mutex
	created []events.Event
}

func (f *fakeEvents) CreateEvent(_ context.Context, in events.CreateInput) (*events.Event, error) {
	if err := events.Validate(in); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	ev := events.Event{ID: int64(len(f.created) + 1), Game: in.Game, Env: in.Env, Name: in.Name,
		StartsAt: in.StartsAt, EndsAt: in.EndsAt, Payload: in.Payload, State: "scheduled", CreatedBy: in.Actor}
	f.created = append(f.created, ev)
	return &ev, nil
}

func (f *fakeEvents) ListEvents(context.Context, string, string, int) ([]events.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.created, nil
}

func (f *fakeEvents) Fires(_ context.Context, id int64) ([]events.Fire, error) {
	return []events.Fire{{EventID: id, FireAt: time.Now().UTC(), Status: "pending"}}, nil
}

type fakeAudit struct {
	mu      sync.Mutex
	entries []auditlog.Entry
}

func (f *fakeAudit) Audit(_ context.Context, e auditlog.Entry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, e)
	return nil
}

func (f *fakeAudit) AuditPage(context.Context, string, int64, int) ([]auditlog.Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.entries, nil
}

func (f *fakeAudit) find(action string) []auditlog.Entry {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []auditlog.Entry
	for _, e := range f.entries {
		if e.Action == action {
			out = append(out, e)
		}
	}
	return out
}

type fakeStats struct{ snap metrics.Snapshot }

func (f *fakeStats) Stats(context.Context) (metrics.Snapshot, error) { return f.snap, nil }

type harness struct {
	srv    *httptest.Server
	config *fakeConfig
	events *fakeEvents
	audit  *fakeAudit
	stats  *fakeStats
	tokens map[string]auth.Principal
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	tokens, err := auth.ParseTokens("admin-token:em:admin,op-token:ci:operator,view-token:viewer:viewer")
	if err != nil {
		t.Fatalf("tokens: %v", err)
	}
	h := &harness{config: newFakeConfig(), events: &fakeEvents{}, audit: &fakeAudit{}, stats: &fakeStats{}, tokens: tokens}
	s := &Server{
		Config: h.config, Events: h.events, Audit: h.audit, Stats: h.stats,
		Tokens: tokens, Envs: []string{"dev", "staging", "prod"}, Started: time.Now(),
	}
	h.srv = httptest.NewServer(s.Handler())
	t.Cleanup(h.srv.Close)
	return h
}

func (h *harness) do(t *testing.T, method, path, token, body string, headers ...string) (*http.Response, string) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, h.srv.URL+path, r)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, string(raw)
}

func TestV1RequiresAToken(t *testing.T) {
	h := newHarness(t)
	resp, body := h.do(t, "GET", "/v1/config?game=demo&env=dev&key=k", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
	if !strings.Contains(body, "Bearer") {
		t.Fatalf("the 401 must say how to authenticate: %s", body)
	}
	resp, _ = h.do(t, "GET", "/v1/config?game=demo&env=dev&key=k", "not-a-token", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("an unknown token is a 401, not a 403: got %d", resp.StatusCode)
	}
}

// Probes and metrics are reachable without a token on purpose: a kubelet has no token.
func TestProbesAndMetricsAreOpen(t *testing.T) {
	h := newHarness(t)
	h.stats.snap = metrics.Snapshot{Effects: 1}
	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		resp, body := h.do(t, "GET", path, "", "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: want 200, got %d", path, resp.StatusCode)
		}
		if path == "/metrics" && !strings.Contains(body, "liveops_event_effects_total 1") {
			t.Fatalf("metrics body unexpected: %s", body)
		}
	}
}

func TestPublisherFlowAndETag(t *testing.T) {
	h := newHarness(t)

	resp, body := h.do(t, "PUT", "/v1/config", "op-token", `{"game":"demo","env":"dev","key":"feature.double_xp","value":{"enabled":false},"note":"start off"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publish: want 200, got %d (%s)", resp.StatusCode, body)
	}
	if got := resp.Header.Get("ETag"); got != `"v1"` {
		t.Fatalf("want ETag \"v1\", got %q", got)
	}

	// The client that already holds v1 gets no body at all.
	resp, _ = h.do(t, "GET", "/v1/config?game=demo&env=dev&key=feature.double_xp", "view-token", "", "If-None-Match", `"v1"`)
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("want 304, got %d", resp.StatusCode)
	}

	resp, body = h.do(t, "PUT", "/v1/config", "op-token", `{"game":"demo","env":"dev","key":"feature.double_xp","value":{"enabled":true}}`, "If-Match", `"v1"`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second publish: want 200, got %d (%s)", resp.StatusCode, body)
	}
	if got := resp.Header.Get("ETag"); got != `"v2"` {
		t.Fatalf("want ETag \"v2\", got %q", got)
	}
}

// The conflict path is the one a panel hits in real life: two editors, one key.
func TestStalePublishIsAConflictThatNamesTheWinner(t *testing.T) {
	h := newHarness(t)
	h.do(t, "PUT", "/v1/config", "op-token", `{"game":"demo","env":"dev","key":"k","value":{"v":1}}`)
	h.do(t, "PUT", "/v1/config", "op-token", `{"game":"demo","env":"dev","key":"k","value":{"v":2}}`, "If-Match", `"v1"`)

	resp, body := h.do(t, "PUT", "/v1/config", "op-token", `{"game":"demo","env":"dev","key":"k","value":{"v":3}}`, "If-Match", `"v1"`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("want 409, got %d (%s)", resp.StatusCode, body)
	}
	var parsed struct {
		CurrentVersion int `json:"current_version"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("conflict body must be JSON: %s", body)
	}
	if parsed.CurrentVersion != 2 {
		t.Fatalf("the body must name the winning version, got %d", parsed.CurrentVersion)
	}
	if denials := h.audit.find("conflict"); len(denials) != 1 {
		t.Fatalf("a conflict must be in the trail, found %d", len(denials))
	}
}

func TestRollbackAppendsWithoutRewritingHistory(t *testing.T) {
	h := newHarness(t)
	h.do(t, "PUT", "/v1/config", "op-token", `{"game":"demo","env":"dev","key":"k","value":{"enabled":false}}`)
	h.do(t, "PUT", "/v1/config", "op-token", `{"game":"demo","env":"dev","key":"k","value":{"enabled":true}}`, "If-Match", `"v1"`)

	resp, body := h.do(t, "POST", "/v1/config/rollback", "op-token", `{"game":"demo","env":"dev","key":"k","to_version":1}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rollback: want 200, got %d (%s)", resp.StatusCode, body)
	}
	if got := resp.Header.Get("ETag"); got != `"v3"` {
		t.Fatalf("a rollback is a new version: want \"v3\", got %q", got)
	}
	var rev remoteconfig.Revision
	if err := json.Unmarshal([]byte(body), &rev); err != nil {
		t.Fatalf("rollback body: %v", err)
	}
	if !bytes.Contains(rev.Value, []byte("false")) {
		t.Fatalf("the rollback must carry the old payload, got %s", rev.Value)
	}
	revs, _ := h.config.Revisions(context.Background(), "demo", "dev", "k", 10)
	if len(revs) != 3 {
		t.Fatalf("history must have grown to 3, has %d", len(revs))
	}
}

// The rule this project exists to demonstrate: the same write is allowed in dev and
// refused in prod for the same role, and the refusal is in the trail with a reason.
func TestProductionWritesNeedAnAdmin(t *testing.T) {
	h := newHarness(t)
	resp, body := h.do(t, "PUT", "/v1/config", "op-token", `{"game":"demo","env":"prod","key":"k","value":{"v":1}}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("want 403, got %d (%s)", resp.StatusCode, body)
	}
	if !strings.Contains(body, "requires role") {
		t.Fatalf("the refusal must name the rule: %s", body)
	}
	denials := h.audit.find(auditlog.ActionDenied)
	if len(denials) != 1 {
		t.Fatalf("the refusal must be recorded, found %d", len(denials))
	}
	if denials[0].Outcome != auditlog.OutcomeDenied || denials[0].Actor != "ci" {
		t.Fatalf("denial entry wrong: %+v", denials[0])
	}
	// Reading production is still allowed: the rule guards writes.
	if resp, _ := h.do(t, "GET", "/v1/config?game=demo&env=prod&key=k", "op-token", ""); resp.StatusCode == http.StatusForbidden {
		t.Fatal("an operator must be able to read production configuration")
	}
	// And an admin can do the write.
	if resp, body := h.do(t, "PUT", "/v1/config", "admin-token", `{"game":"demo","env":"prod","key":"k","value":{"v":1}}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("admin publish: want 200, got %d (%s)", resp.StatusCode, body)
	}
}

func TestViewerCannotPublishButCanRead(t *testing.T) {
	h := newHarness(t)
	if resp, _ := h.do(t, "PUT", "/v1/config", "view-token", `{"game":"demo","env":"dev","key":"k","value":{}}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer write: want 403, got %d", resp.StatusCode)
	}
	if resp, _ := h.do(t, "GET", "/v1/audit?limit=5", "view-token", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("viewer audit read: want 200, got %d", resp.StatusCode)
	}
}

func TestEventValidationIsABadRequest(t *testing.T) {
	h := newHarness(t)
	body := `{"game":"demo","env":"dev","name":"too fast","starts_at":"2026-10-12T10:00:00Z","ends_at":"2026-10-12T11:00:00Z","repeat_every_secs":30}`
	resp, out := h.do(t, "POST", "/v1/events", "op-token", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d (%s)", resp.StatusCode, out)
	}
	if !strings.Contains(out, "at least 60") {
		t.Fatalf("the 400 must explain the floor: %s", out)
	}
	if !strings.Contains(out, "invalid event") {
		t.Fatalf("the 400 must be recognisably a validation failure: %s", out)
	}
}

func TestMissingKeyIs404(t *testing.T) {
	h := newHarness(t)
	resp, body := h.do(t, "GET", "/v1/config?game=demo&env=dev&key=absent", "view-token", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d (%s)", resp.StatusCode, body)
	}
}

func TestWhoamiDescribesTheCaller(t *testing.T) {
	h := newHarness(t)
	resp, body := h.do(t, "GET", "/v1/whoami", "op-token", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var out struct {
		Actor       string   `json:"actor"`
		Role        string   `json:"role"`
		Permissions []string `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("whoami body: %v", err)
	}
	if out.Actor != "ci" || out.Role != "operator" {
		t.Fatalf("whoami wrong: %+v", out)
	}
	// The panel uses this list to hide what the server would refuse; if prod publish is
	// missing it will not offer the button, which is the point.
	for _, p := range out.Permissions {
		if p == string(auth.PermConfigPublish) {
			return
		}
	}
	t.Fatalf("publish should be listed for an operator: %+v", out.Permissions)
}
