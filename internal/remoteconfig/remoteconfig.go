// Package remoteconfig holds the parts of configuration that are decided in Go rather
// than in SQL: how a version is addressed over HTTP, and what a publish is allowed to
// mean. The storage rules live in internal/pgstore, but the contract lives here.
package remoteconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrConflict means the caller's expectation about the current version was wrong.
	// It is deliberately not a 500: a conflict is the system working.
	ErrConflict = errors.New("version conflict")
	ErrNotFound = errors.New("not found")
	// ErrInvalid marks a caller mistake, so the API can answer 400 rather than 500.
	ErrInvalid = errors.New("invalid request")
)

// Revision is one immutable record in the history of a (game, env, key) triple.
type Revision struct {
	ID        int64           `json:"id"`
	Game      string          `json:"game"`
	Env       string          `json:"env"`
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	Version   int             `json:"version"`
	Note      string          `json:"note"`
	CreatedBy string          `json:"created_by"`
	CreatedAt time.Time       `json:"created_at"`
}

// PublishInput is a write. ExpectVersion and FromVersion are the two ways a caller can
// describe the history it believes it is writing on top of.
type PublishInput struct {
	Game string
	Env  string
	Key  string
	// Value is used when FromVersion is nil.
	Value json.RawMessage
	Note  string
	Actor string
	// ExpectVersion is optimistic concurrency: if the current version differs, the
	// write fails with ErrConflict instead of silently overwriting someone else.
	ExpectVersion *int
	// FromVersion is a rollback: take the payload of that version and publish it as a
	// new one. History is appended to, never rewritten.
	FromVersion *int
}

// ETag renders a version as an HTTP entity tag.
func ETag(version int) string { return fmt.Sprintf("%q", "v"+strconv.Itoa(version)) }

// ParseETag accepts the forms a client may echo back: "v17", W/"v17", 17.
func ParseETag(s string) (int, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "W/")
	s = strings.Trim(s, `"`)
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return 0, errors.New("empty etag")
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return 0, fmt.Errorf("not a version: %q", s)
	}
	return v, nil
}

// Validate checks the shape of a key without touching the store.
func Validate(game, env, key string) error {
	if game == "" || env == "" || key == "" {
		return fmt.Errorf("%w: game, env and key are required", ErrInvalid)
	}
	for _, part := range []string{game, env, key} {
		if strings.ContainsAny(part, " \t\n/") {
			return fmt.Errorf("%w: %q may not contain whitespace or /", ErrInvalid, part)
		}
	}
	return nil
}
