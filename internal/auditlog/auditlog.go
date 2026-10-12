// Package auditlog defines the one shape every change writes: who, what, on which
// target, and whether it was allowed. Denials are recorded as well as successes,
// because "who tried and could not" is the question asked after an incident, and the
// answer has to be in the same place as the successes.
package auditlog

import (
	"encoding/json"
	"time"
)

type Outcome string

const (
	OutcomeOK       Outcome = "ok"
	OutcomeDenied   Outcome = "denied"
	OutcomeConflict Outcome = "conflict"
)

type Entry struct {
	ID        int64           `json:"id"`
	At        time.Time       `json:"at"`
	Actor     string          `json:"actor"`
	Action    string          `json:"action"`
	Target    string          `json:"target"`
	Outcome   Outcome         `json:"outcome"`
	Before    json.RawMessage `json:"before,omitempty"`
	After     json.RawMessage `json:"after,omitempty"`
	RequestID string          `json:"request_id,omitempty"`
}

// Actions are strings, not an enum type, on purpose: the audit trail is read by people
// grepping for "config.publish", and a typed constant buys nothing at that boundary.
const (
	ActionConfigPublish  = "config.publish"
	ActionConfigRollback = "config.rollback"
	ActionEventCreate    = "event.create"
	ActionEventFire      = "event.fire"
	ActionDenied         = "access.denied"
)
