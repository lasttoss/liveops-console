package remoteconfig

import "testing"

func TestETagRoundTrip(t *testing.T) {
	for _, v := range []int{1, 17, 1000} {
		got, err := ParseETag(ETag(v))
		if err != nil {
			t.Fatalf("ParseETag(ETag(%d)): %v", v, err)
		}
		if got != v {
			t.Fatalf("round trip: want %d, got %d", v, got)
		}
	}
}

func TestParseETagAcceptsClientForms(t *testing.T) {
	for _, in := range []string{`"v3"`, `W/"v3"`, "3", "v3"} {
		got, err := ParseETag(in)
		if err != nil || got != 3 {
			t.Fatalf("%q: want 3, got %d (%v)", in, got, err)
		}
	}
}

func TestParseETagRejectsGarbage(t *testing.T) {
	// "v0" and "" matter most: a client that sends one must be told it is wrong rather
	// than being treated as "no expectation", which would silently skip the conflict
	// check and overwrite a concurrent publish.
	for _, in := range []string{"", `""`, "v0", "0", `W/"abc"`, "-2"} {
		if v, err := ParseETag(in); err == nil {
			t.Fatalf("%q should not parse, got %d", in, v)
		}
	}
}

func TestValidate(t *testing.T) {
	if err := Validate("demo", "dev", "feature.x"); err != nil {
		t.Fatalf("valid key rejected: %v", err)
	}
	for _, in := range [][3]string{{"", "dev", "k"}, {"demo", "", "k"}, {"demo", "dev", ""}, {"de mo", "dev", "k"}, {"demo", "dev", "a/b"}} {
		if err := Validate(in[0], in[1], in[2]); err == nil {
			t.Fatalf("invalid triple accepted: %v", in)
		}
	}
}
