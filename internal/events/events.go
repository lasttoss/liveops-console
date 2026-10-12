// Package events turns a schedule into the list of instants that must happen exactly
// once. The list is computed before anything is stored so that the interesting bug -
// the same reward handed out twice - is decided by a primary key rather than by luck.
package events

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrInvalid marks a caller mistake. The API maps it to 400, which is why it exists:
// a validation failure that arrives as a 500 teaches a client to retry something that
// will never work.
var ErrInvalid = errors.New("invalid event")

// MinRepeat keeps a typo like "every 5 seconds" from becoming a retry storm: the
// database enforces the same floor, and this is the message the caller actually sees.
const MinRepeat = time.Minute

// MaxFires bounds one event. A daily event for ten years is a schema mistake, not a
// schedule, and materialising it would fill a table nobody reads.
const MaxFires = 500

type Event struct {
	ID              int64           `json:"id"`
	Game            string          `json:"game"`
	Env             string          `json:"env"`
	Name            string          `json:"name"`
	StartsAt        time.Time       `json:"starts_at"`
	EndsAt          time.Time       `json:"ends_at"`
	RepeatEverySecs *int            `json:"repeat_every_secs,omitempty"`
	Payload         json.RawMessage `json:"payload"`
	State           string          `json:"state"`
	CreatedBy       string          `json:"created_by"`
	CreatedAt       time.Time       `json:"created_at"`
}

type Fire struct {
	EventID     int64      `json:"event_id"`
	FireAt      time.Time  `json:"fire_at"`
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	EffectCount int        `json:"effect_count"`
	ClaimedAt   *time.Time `json:"claimed_at,omitempty"`
	ClaimedBy   *string    `json:"claimed_by,omitempty"`
}

type CreateInput struct {
	Game            string
	Env             string
	Name            string
	StartsAt        time.Time
	EndsAt          time.Time
	RepeatEverySecs *int
	Payload         json.RawMessage
	Actor           string
}

func Validate(in CreateInput) error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, args...)...)
	}
	if in.Game == "" || in.Env == "" {
		return bad("game and env are required")
	}
	if in.Name == "" {
		return bad("name is required")
	}
	if in.EndsAt.IsZero() || in.StartsAt.IsZero() {
		return bad("starts_at and ends_at are required")
	}
	if !in.EndsAt.After(in.StartsAt) {
		return bad("ends_at must be after starts_at")
	}
	if in.RepeatEverySecs != nil {
		if *in.RepeatEverySecs < int(MinRepeat.Seconds()) {
			return bad("repeat_every_secs must be at least 60")
		}
	}
	if len(in.Payload) > 0 && !json.Valid(in.Payload) {
		return bad("payload must be valid JSON")
	}
	if n := len(Fires(in.StartsAt, in.EndsAt, in.Repeat(), MaxFires)); n == 0 {
		return bad("the schedule produces no fire")
	}
	return nil
}

func (in CreateInput) Repeat() time.Duration {
	if in.RepeatEverySecs == nil {
		return 0
	}
	return time.Duration(*in.RepeatEverySecs) * time.Second
}

// Fires returns every instant the event must fire: once at its start when it does not
// repeat, otherwise at the start and every interval up to and including the end.
// Capped at max, and the cap is reported by the caller rather than silently dropped.
func Fires(startsAt, endsAt time.Time, repeatEvery time.Duration, max int) []time.Time {
	if startsAt.IsZero() || endsAt.IsZero() || endsAt.Before(startsAt) {
		return nil
	}
	if max <= 0 {
		max = MaxFires
	}
	if repeatEvery < MinRepeat {
		// A single fire. Note this also covers a caller that asked for a repeat shorter
		// than the floor: Validate rejects that, and here it degrades to sane.
		return []time.Time{startsAt}
	}
	out := make([]time.Time, 0, 8)
	for t := startsAt; !t.After(endsAt); t = t.Add(repeatEvery) {
		out = append(out, t)
		if len(out) == max {
			break
		}
	}
	return out
}
