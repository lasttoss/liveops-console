// The UI half of the authorisation rule in internal/auth. The server is the authority -
// this exists so the panel hides a button it knows would be refused instead of offering
// it and then failing. A divergence between the two is a bug in this file, not a
// security hole, which is the right way round.

export const PRODUCTION_ENVS = ["prod", "production"];

export function isProduction(env: string): boolean {
  return PRODUCTION_ENVS.includes(env);
}

export function permissionsFor(role: string): string[] {
  switch (role) {
    case "viewer":
      return ["config:read", "events:read", "audit:read"];
    case "operator":
      return ["config:read", "events:read", "audit:read", "config:publish", "config:rollback", "events:write"];
    case "admin":
      return [
        "config:read",
        "events:read",
        "audit:read",
        "config:publish",
        "config:rollback",
        "events:write",
      ];
    default:
      return [];
  }
}

// The permissions the environment rule guards. Reading production configuration is how
// an operator diagnoses an incident, so reads are not guarded - the same distinction the
// server makes in internal/auth.IsWrite, and the test below is what keeps the two files
// saying the same thing. It caught this exact divergence once already.
const WRITE_PERMISSIONS = ["config:publish", "config:rollback", "events:write"];

export function canWrite(role: string, permission: string, env: string): boolean {
  if (!permissionsFor(role).includes(permission)) return false;
  if (WRITE_PERMISSIONS.includes(permission) && isProduction(env) && role !== "admin") return false;
  return true;
}

// The message a person needs when the button is refusing to appear.
export function whyRefused(role: string, permission: string, env: string): string | null {
  if (canWrite(role, permission, env)) return null;
  if (!permissionsFor(role).includes(permission)) return `role "${role}" does not hold ${permission}`;
  return `${permission} in "${env}" requires role "admin", not "${role}"`;
}
