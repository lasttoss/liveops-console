package metrics

import (
	"strings"
	"testing"
)

func TestRenderHasHelpAndTypeForEveryCounter(t *testing.T) {
	out := Render(Snapshot{Revisions: 3, Effects: 1, Denials: 2})
	for _, c := range counters(Snapshot{}) {
		if !strings.Contains(out, "# HELP "+c.name+" ") {
			t.Errorf("%s has no HELP line", c.name)
		}
		if !strings.Contains(out, "# TYPE "+c.name+" gauge") {
			t.Errorf("%s has no TYPE line", c.name)
		}
	}
	// The value must be on its own line with a trailing newline, which is what a
	// scraper reads and what a hand-written renderer gets wrong first.
	if !strings.Contains(out, "\nliveops_event_effects_total 1\n") {
		t.Fatalf("value line missing or malformed:\n%s", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Fatal("the exposition must end with a newline")
	}
}

func TestRenderSeparatesCountersByTheirOwnHelpBlock(t *testing.T) {
	out := Render(Snapshot{Revisions: 7})
	if !strings.Contains(out, "# HELP liveops_config_revisions_total Configuration revisions appended") ||
		!strings.Contains(out, "\nliveops_config_revisions_total 7\n") {
		t.Fatalf("unexpected rendering:\n%s", out)
	}
}
