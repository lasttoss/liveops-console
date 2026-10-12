package main

// The demo client is mostly exercised end to end by the `demo` CI job, which greps the
// story it prints. What is worth a unit test is the parsing it does of its own API's
// output, because a wrong reading there would make the story say something false.

import "testing"

func TestCounterValue(t *testing.T) {
	body := "# HELP liveops_event_effects_total Effects produced by the worker.\n" +
		"# TYPE liveops_event_effects_total gauge\n" +
		"liveops_event_effects_total 1\n" +
		"liveops_events 2\n"
	if got := counterValue(body, "liveops_event_effects_total"); got != "1" {
		t.Fatalf("want 1, got %q", got)
	}
	if got := counterValue(body, "liveops_events"); got != "2" {
		t.Fatalf("want 2, got %q", got)
	}
	// A missing counter must be visibly missing rather than silently zero: the demo says
	// "effects = ?" and the CI grep for "effects = 1" then fails, which is the point.
	if got := counterValue(body, "liveops_not_there"); got != "?" {
		t.Fatalf("want ?, got %q", got)
	}
	// The HELP line starts with the name too, so the value must come from the line that
	// has a number after the name.
	if got := counterValue("# HELP x\ny 3\n", "y"); got != "3" {
		t.Fatalf("want 3, got %q", got)
	}
}

func TestEnvOr(t *testing.T) {
	t.Setenv("LIVEOPS_API", "")
	if got := envOr("LIVEOPS_API", "http://localhost:18100"); got != "http://localhost:18100" {
		t.Fatalf("empty must fall back, got %q", got)
	}
	t.Setenv("LIVEOPS_API", "http://api:8080")
	if got := envOr("LIVEOPS_API", "fallback"); got != "http://api:8080" {
		t.Fatalf("set must win, got %q", got)
	}
}
