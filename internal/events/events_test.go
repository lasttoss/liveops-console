package events

import (
	"testing"
	"time"
)

func at(hour, min int) time.Time {
	return time.Date(2026, 10, 12, hour, min, 0, 0, time.UTC)
}

func TestFiresOnce(t *testing.T) {
	got := Fires(at(10, 0), at(12, 0), 0, 0)
	if len(got) != 1 || !got[0].Equal(at(10, 0)) {
		t.Fatalf("a non-repeating event fires once at its start, got %v", got)
	}
}

func TestFiresInclusiveOfEnd(t *testing.T) {
	// 10:00 to 10:05 every minute is 10:00, 10:01, 10:02, 10:03, 10:04, 10:05.
	got := Fires(at(10, 0), at(10, 5), time.Minute, 0)
	if len(got) != 6 {
		t.Fatalf("want 6 fires, got %d: %v", len(got), got)
	}
	if !got[5].Equal(at(10, 5)) {
		t.Fatalf("the end instant must be included, got %v", got[5])
	}
}

func TestFiresRespectsTheCap(t *testing.T) {
	got := Fires(at(0, 0), at(23, 59), time.Minute, 10)
	if len(got) != 10 {
		t.Fatalf("cap ignored: got %d", len(got))
	}
}

func TestFiresRejectsImpossibleWindow(t *testing.T) {
	if got := Fires(at(12, 0), at(10, 0), 0, 0); got != nil {
		t.Fatalf("end before start must produce nothing, got %v", got)
	}
	if got := Fires(time.Time{}, at(10, 0), 0, 0); got != nil {
		t.Fatalf("zero start must produce nothing, got %v", got)
	}
}

func TestValidate(t *testing.T) {
	base := func() CreateInput {
		return CreateInput{Game: "demo", Env: "dev", Name: "weekly", StartsAt: at(10, 0), EndsAt: at(11, 0)}
	}
	if err := Validate(base()); err != nil {
		t.Fatalf("valid event rejected: %v", err)
	}
	short := 30
	in := base()
	in.RepeatEverySecs = &short
	if err := Validate(in); err == nil {
		t.Fatal("a 30s repeat must be rejected")
	}
	ok := 60
	in = base()
	in.RepeatEverySecs = &ok
	if err := Validate(in); err != nil {
		t.Fatalf("a 60s repeat must be accepted: %v", err)
	}
	in = base()
	in.EndsAt = in.StartsAt
	if err := Validate(in); err == nil {
		t.Fatal("zero-length window must be rejected")
	}
	in = base()
	in.Payload = []byte("{not json")
	if err := Validate(in); err == nil {
		t.Fatal("invalid payload must be rejected")
	}
	in = base()
	in.Name = ""
	if err := Validate(in); err == nil {
		t.Fatal("missing name must be rejected")
	}
}
