// Command demo is the two-minute version of this repository: it drives the public API
// the way a panel would, and prints what happened. It is a client, not a test - the
// tests assert, this explains. The demo is also the thing the CI job greps, so its
// output wording is load-bearing.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type client struct {
	base  string
	token string
	http  *http.Client
}

type response struct {
	status int
	body   string
	etag   string
}

func (c *client) do(method, path, body string, headers ...string) response {
	var r io.Reader
	if body != "" {
		r = bytes.NewReader([]byte(body))
	}
	req, err := http.NewRequest(method, c.base+path, r)
	if err != nil {
		panic(err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := c.http.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot reach the API at %s: %v\n", c.base, err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return response{status: resp.StatusCode, body: string(raw), etag: resp.Header.Get("ETag")}
}

func main() {
	base := flag.String("api", envOr("LIVEOPS_API", "http://localhost:8080"), "base URL of the running API")
	report := flag.Bool("report", false, "do not write anything: print the counters and the audit trail")
	flag.Parse()

	admin := client{base: *base, token: envOr("LIVEOPS_ADMIN_TOKEN", "admin-token"), http: &http.Client{Timeout: 10 * time.Second}}
	operator := client{base: *base, token: envOr("LIVEOPS_OPERATOR_TOKEN", "op-token"), http: &http.Client{Timeout: 10 * time.Second}}
	viewer := client{base: *base, token: envOr("LIVEOPS_VIEWER_TOKEN", "view-token"), http: &http.Client{Timeout: 10 * time.Second}}

	if *report {
		reportTail(admin)
		return
	}
	story(admin, operator, viewer)
}

func story(admin, operator, viewer client) {
	fmt.Println("liveops-console - a change you can undo, and a reward that happens once")
	fmt.Println()

	fmt.Println("1. who am I")
	who := operator.do("GET", "/v1/whoami", "")
	pretty(who.body)
	fmt.Println()

	const key = "feature.double_xp"
	fmt.Println("2. publish a flag: config is append-only, so this is revision 1")
	first := operator.do("PUT", "/v1/config", `{"game":"demo","env":"dev","key":"`+key+`","value":{"enabled":false,"multiplier":1},"note":"ship dark"}`)
	fmt.Printf("   PUT /v1/config -> %d  ETag: %s\n", first.status, first.etag)

	fmt.Println("3. a client that already holds v1 asks again")
	notModified := viewer.do("GET", "/v1/config?game=demo&env=dev&key="+key, "", "If-None-Match", first.etag)
	fmt.Printf("   GET with If-None-Match -> %d (no body sent)\n\n", notModified.status)

	fmt.Println("4. turn it on, on top of the version we read")
	second := operator.do("PUT", "/v1/config", `{"game":"demo","env":"dev","key":"`+key+`","value":{"enabled":true,"multiplier":2},"note":"launch"}`, "If-Match", first.etag)
	fmt.Printf("   PUT /v1/config with If-Match: %s -> %d  ETag: %s\n\n", first.etag, second.status, second.etag)

	fmt.Println("5. a second editor still holds v1 and writes anyway")
	stale := operator.do("PUT", "/v1/config", `{"game":"demo","env":"dev","key":"`+key+`","value":{"enabled":"loses"},"note":"stale"}`, "If-Match", first.etag)
	fmt.Printf("   PUT /v1/config with a stale If-Match -> %d (the loser is told which version won)\n", stale.status)
	pretty(stale.body)
	fmt.Println()

	fmt.Println("6. roll back to v1: history is kept, a new revision is appended")
	rollback := operator.do("POST", "/v1/config/rollback", `{"game":"demo","env":"dev","key":"`+key+`","to_version":1,"note":"bad multiplier"}`)
	fmt.Printf("   POST /v1/config/rollback -> %d  ETag: %s\n", rollback.status, rollback.etag)
	pretty(rollback.body)
	fmt.Println()

	fmt.Println("7. schedule a reward that already fired, so the worker has something due")
	start := time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339)
	end := time.Now().UTC().Add(-1 * time.Minute).Format(time.RFC3339)
	event := operator.do("POST", "/v1/events", `{"game":"demo","env":"dev","name":"weekly_quest","starts_at":"`+start+`","ends_at":"`+end+`","payload":{"reward":"100 coins"}}`)
	fmt.Printf("   POST /v1/events -> %d (the schedule is materialised now, not when it fires)\n", event.status)
	fmt.Println()

	fmt.Println("8. a viewer tries to publish")
	denied := viewer.do("PUT", "/v1/config", `{"game":"demo","env":"dev","key":"`+key+`","value":{}}`)
	fmt.Printf("   PUT /v1/config as viewer -> %d\n", denied.status)
	pretty(denied.body)
	fmt.Println()

	fmt.Println("9. prod is guarded by role, not by convention")
	prodDenied := operator.do("PUT", "/v1/config", `{"game":"demo","env":"prod","key":"`+key+`","value":{"enabled":true}}`)
	fmt.Printf("   PUT /v1/config as operator in prod -> %d\n", prodDenied.status)
	pretty(prodDenied.body)
	fmt.Println()

	fmt.Println("next: run the worker twice, then `demo --report`")
}

func reportTail(admin client) {
	fmt.Println("10. counters after the workers ran")
	metrics := admin.do("GET", "/metrics", "")
	for _, line := range strings.Split(metrics.body, "\n") {
		if strings.HasPrefix(line, "liveops_") {
			fmt.Printf("   %s\n", line)
		}
	}
	fmt.Println()

	fmt.Println("11. the audit trail: successes and refusals in the same table, newest first")
	trail := admin.do("GET", "/v1/audit?limit=8", "")
	var parsed struct {
		Entries []struct {
			Actor   string `json:"actor"`
			Action  string `json:"action"`
			Target  string `json:"target"`
			Outcome string `json:"outcome"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(trail.body), &parsed); err != nil {
		fmt.Fprintf(os.Stderr, "cannot read the trail: %v\n", err)
		os.Exit(1)
	}
	for _, e := range parsed.Entries {
		fmt.Printf("   %-8s %-18s %-28s %s\n", e.Actor, e.Action, e.Target, e.Outcome)
	}
	fmt.Println()

	fmt.Println("12. exactly-once, visibly")
	effects := counterValue(metrics.body, "liveops_event_effects_total")
	fmt.Printf("   two worker passes, one fire, effects = %s\n", effects)
	if effects == "1" {
		fmt.Println("   the second pass found nothing pending: the claim and the effect are the same UPDATE")
	} else {
		fmt.Printf("   UNEXPECTED: effects should be 1, not %s\n", effects)
	}
}

func counterValue(body, name string) string {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, name+" ") {
			return strings.TrimSpace(strings.TrimPrefix(line, name))
		}
	}
	return "?"
}

func pretty(body string) {
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(body), "   ", "  "); err != nil {
		fmt.Printf("   %s\n", body)
		return
	}
	for _, line := range strings.Split(buf.String(), "\n") {
		fmt.Printf("   %s\n", line)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
