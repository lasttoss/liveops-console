// Package metrics renders the exposition format by hand. There is no client library
// here on purpose: the counters that matter in this service are six SQL counts, and a
// dependency is a worse trade than sixty lines you can read in a review.
package metrics

import (
	"fmt"
	"strings"
)

// Snapshot is what the store can count cheaply. It is a struct rather than a map so
// that adding a counter is a compile error everywhere it has to be filled in.
type Snapshot struct {
	Revisions    int64
	ConfigKeys   int64
	Events       int64
	PendingFires int64
	Effects      int64
	AuditRows    int64
	Denials      int64
}

type counter struct {
	name string
	help string
	val  int64
}

func counters(s Snapshot) []counter {
	return []counter{
		{"liveops_config_revisions_total", "Configuration revisions appended since the database was created.", s.Revisions},
		{"liveops_config_keys", "Distinct (game, env, key) triples that have at least one revision.", s.ConfigKeys},
		{"liveops_events", "Scheduled events.", s.Events},
		{"liveops_event_fires_pending", "Fires that are due or upcoming and not yet claimed.", s.PendingFires},
		{"liveops_event_effects_total", "Effects produced by the worker. Equal to the number of claims, which is the point.", s.Effects},
		{"liveops_audit_rows_total", "Rows in the append-only audit trail.", s.AuditRows},
		{"liveops_access_denials_total", "Authorisation refusals, recorded in the same trail as the successes.", s.Denials},
	}
}

// Render writes the Prometheus text format. HELP and TYPE lines are included because a
// counter without a description is a number nobody can act on.
func Render(s Snapshot) string {
	var b strings.Builder
	for _, c := range counters(s) {
		fmt.Fprintf(&b, "# HELP %s %s\n", c.name, c.help)
		fmt.Fprintf(&b, "# TYPE %s gauge\n", c.name)
		fmt.Fprintf(&b, "%s %d\n", c.name, c.val)
	}
	return b.String()
}
