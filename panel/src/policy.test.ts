import { describe, expect, it } from "vitest";
import { canWrite, permissionsFor, whyRefused } from "./policy";

describe("the panel's copy of the authorisation rule", () => {
  it("lets an operator publish in dev", () => {
    expect(canWrite("operator", "config:publish", "dev")).toBe(true);
  });

  it("stops an operator publishing in prod, for the same permission", () => {
    expect(canWrite("operator", "config:publish", "prod")).toBe(false);
    expect(whyRefused("operator", "config:publish", "prod")).toContain("requires role");
  });

  it("still lets an operator read prod: the rule guards writes", () => {
    expect(canWrite("operator", "config:read", "prod")).toBe(true);
  });

  it("never lets a viewer write anything anywhere", () => {
    for (const env of ["dev", "staging", "prod"]) {
      expect(canWrite("viewer", "config:publish", env)).toBe(false);
      expect(canWrite("viewer", "config:rollback", env)).toBe(false);
      expect(canWrite("viewer", "events:write", env)).toBe(false);
    }
  });

  it("gives an admin everything the operator has, plus production writes", () => {
    const op = new Set(permissionsFor("operator"));
    for (const p of op) expect(permissionsFor("admin")).toContain(p);
    expect(canWrite("admin", "config:rollback", "prod")).toBe(true);
  });

  it("names the missing permission rather than the environment when both apply", () => {
    expect(whyRefused("viewer", "config:publish", "prod")).toBe('role "viewer" does not hold config:publish');
  });
});
