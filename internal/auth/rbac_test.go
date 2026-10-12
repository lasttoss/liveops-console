package auth

import "testing"

// The matrix is written out rather than derived from the implementation: a test that
// asks Can() what it should say is a test of nothing.
func TestPermissionMatrix(t *testing.T) {
	cases := []struct {
		role string
		perm Permission
		want bool
	}{
		{RoleViewer, PermConfigRead, true},
		{RoleViewer, PermEventsRead, true},
		{RoleViewer, PermAuditRead, true},
		{RoleViewer, PermConfigPublish, false},
		{RoleViewer, PermConfigRollback, false},
		{RoleViewer, PermEventsWrite, false},
		{RoleOperator, PermConfigRead, true},
		{RoleOperator, PermConfigPublish, true},
		{RoleOperator, PermConfigRollback, true},
		{RoleOperator, PermEventsWrite, true},
		{RoleOperator, PermAuditRead, true},
		{RoleAdmin, PermConfigPublish, true},
		{RoleAdmin, PermEventsWrite, true},
		{"intern", PermConfigRead, false},
	}
	for _, c := range cases {
		if got := Can(c.role, c.perm); got != c.want {
			t.Errorf("Can(%s, %s) = %v, want %v", c.role, c.perm, got, c.want)
		}
	}
}

// The rule this package exists for: the same permission is allowed in dev and refused
// in prod for the same role.
func TestProductionNeedsAdmin(t *testing.T) {
	op := Principal{Actor: "em", Role: RoleOperator}
	if err := Authorize(op, PermConfigPublish, "dev"); err != nil {
		t.Fatalf("operator should publish in dev: %v", err)
	}
	if err := Authorize(op, PermConfigPublish, "prod"); err == nil {
		t.Fatal("operator must not publish in prod")
	}
	admin := Principal{Actor: "em", Role: RoleAdmin}
	if err := Authorize(admin, PermConfigPublish, "prod"); err != nil {
		t.Fatalf("admin should publish in prod: %v", err)
	}
	// A read in prod stays open: the extra rule guards writes.
	if err := Authorize(op, PermConfigRead, "prod"); err != nil {
		t.Fatalf("operator should read in prod: %v", err)
	}
}

// A 403 that does not say which rule fired costs the next reader an hour.
func TestAuthorizeExplainsItself(t *testing.T) {
	err := Authorize(Principal{Actor: "em", Role: RoleOperator}, PermConfigPublish, "prod")
	if err == nil || !contains(err.Error(), "requires role") {
		t.Fatalf("expected the environment rule to be named, got %v", err)
	}
	err = Authorize(Principal{Actor: "em", Role: RoleViewer}, PermConfigPublish, "dev")
	if err == nil || !contains(err.Error(), "does not hold") {
		t.Fatalf("expected the missing permission to be named, got %v", err)
	}
	err = Authorize(Principal{}, PermConfigRead, "dev")
	if err == nil || !contains(err.Error(), "no principal") {
		t.Fatalf("expected the missing token to be named, got %v", err)
	}
}

func TestParseTokens(t *testing.T) {
	m, err := ParseTokens(" t1:em:admin, t2:ci:operator ")
	if err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}
	if m["t1"].Actor != "em" || m["t1"].Role != RoleAdmin || m["t2"].Role != RoleOperator {
		t.Fatalf("parsed wrong: %+v", m)
	}
	empty, err := ParseTokens("")
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty spec should mean no tokens, got %v %v", empty, err)
	}
	for _, bad := range []string{"t1:em", "t1:em:root", ":em:admin", "t1::admin", "t1:em:admin,t1:x:viewer"} {
		if _, err := ParseTokens(bad); err == nil {
			t.Fatalf("%q should be rejected", bad)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
