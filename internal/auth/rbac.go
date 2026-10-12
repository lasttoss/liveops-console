// Package auth decides who may change what. Roles and permissions are code, not a
// table: the rule that matters here is environmental (production writes need an
// admin), and a rule you can read in one function is a rule you can test.
//
// An identity provider is deliberately out of scope: actors come from a bearer token
// list in the environment, which is enough to demonstrate the authorisation path
// without shipping a second product inside this one.
package auth

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

type Permission string

const (
	PermConfigRead     Permission = "config:read"
	PermConfigPublish  Permission = "config:publish"
	PermConfigRollback Permission = "config:rollback"
	PermEventsRead     Permission = "events:read"
	PermEventsWrite    Permission = "events:write"
	PermAuditRead      Permission = "audit:read"
)

// Roles, least privileged first.
const (
	RoleViewer   = "viewer"
	RoleOperator = "operator"
	RoleAdmin    = "admin"
)

// Principal is an authenticated actor. Role is a name, not a set of permissions: the
// set is derived below so that a permission can never be granted by editing data.
type Principal struct {
	Actor string
	Role  string
}

// ProductionEnvs are the environments where the extra rule applies.
var ProductionEnvs = map[string]bool{"prod": true, "production": true}

// Can answers the base question: does this role hold the permission at all?
func Can(role string, p Permission) bool {
	switch role {
	case RoleViewer:
		return p == PermConfigRead || p == PermEventsRead || p == PermAuditRead
	case RoleOperator:
		return Can(RoleViewer, p) || p == PermConfigPublish || p == PermConfigRollback || p == PermEventsWrite
	case RoleAdmin:
		return true
	default:
		return false
	}
}

// IsWrite separates the permissions the environment rule guards from the ones it does
// not. Reading production configuration is how an operator diagnoses an incident; a
// rule that also blocked the reads would be a rule people work around.
func IsWrite(p Permission) bool {
	return p == PermConfigPublish || p == PermConfigRollback || p == PermEventsWrite
}

// Authorize is the whole authorisation decision, including the environment rule. The
// returned error is written to the audit trail as a denial, which is why it explains
// itself: a 403 that does not say which rule fired costs the next reader an hour.
func Authorize(p Principal, perm Permission, env string) error {
	if p.Actor == "" || p.Role == "" {
		return errors.New("no principal: send Authorization: Bearer <token>")
	}
	if !Can(p.Role, perm) {
		return fmt.Errorf("role %q does not hold %s", p.Role, perm)
	}
	if IsWrite(perm) && ProductionEnvs[env] && p.Role != RoleAdmin {
		return fmt.Errorf("%s in %q requires role %q, not %q", perm, env, RoleAdmin, p.Role)
	}
	return nil
}

func KnownRole(role string) bool {
	switch role {
	case RoleViewer, RoleOperator, RoleAdmin:
		return true
	}
	return false
}

// ParseTokens reads "token:actor:role" entries, comma separated. The token is the
// secret; the actor is what the audit trail will name, which is the whole point of
// keeping them apart: rotating a token must not rewrite history.
func ParseTokens(spec string) (map[string]Principal, error) {
	out := map[string]Principal{}
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return out, nil
	}
	for _, entry := range strings.Split(spec, ",") {
		parts := strings.Split(strings.TrimSpace(entry), ":")
		if len(parts) != 3 {
			return nil, fmt.Errorf("token spec %q is not token:actor:role", entry)
		}
		token, actor, role := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2])
		if token == "" || actor == "" {
			return nil, fmt.Errorf("token spec %q has an empty token or actor", entry)
		}
		if !KnownRole(role) {
			return nil, fmt.Errorf("unknown role %q in %q", role, entry)
		}
		if _, dup := out[token]; dup {
			return nil, fmt.Errorf("token appears twice in the spec")
		}
		out[token] = Principal{Actor: actor, Role: role}
	}
	return out, nil
}

// PermissionList is used by the API to describe the caller to the panel, so the UI can
// hide what the server would refuse instead of offering it and then failing.
func PermissionList(role string, envs []string) []Permission {
	all := []Permission{PermConfigRead, PermConfigPublish, PermConfigRollback, PermEventsRead, PermEventsWrite, PermAuditRead}
	var out []Permission
	for _, p := range all {
		for _, env := range envs {
			if Authorize(Principal{Actor: "-", Role: role}, p, env) == nil {
				out = append(out, p)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
