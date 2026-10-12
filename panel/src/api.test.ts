import { describe, expect, it, vi } from "vitest";
import { ApiError, ConflictError, Client } from "./api";

function responder(status: number, body: string, headers: Record<string, string> = {}) {
  return vi.fn(async () =>
    new Response(body, { status, headers: { "Content-Type": "application/json", ...headers } }),
  ) as unknown as typeof fetch;
}

describe("the API client", () => {
  it("sends the token and the caller's If-Match", async () => {
    const fetchImpl = responder(200, JSON.stringify({ version: 2 }), { ETag: '"v2"' });
    const client = new Client("op-token", "", fetchImpl);
    await client.publish({ game: "demo", env: "dev", key: "k", value: { a: 1 }, note: "" }, '"v1"');
    const [url, init] = (fetchImpl as unknown as ReturnType<typeof vi.fn>).mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/v1/config");
    expect((init.headers as Record<string, string>)["Authorization"]).toBe("Bearer op-token");
    expect((init.headers as Record<string, string>)["If-Match"]).toBe('"v1"');
  });

  it("turns a 409 into a ConflictError carrying the winning version", async () => {
    const body = JSON.stringify({ error: "version conflict: the current version is 7", current_version: 7 });
    const client = new Client("op-token", "", responder(409, body));
    await expect(client.publish({ game: "demo", env: "dev", key: "k", value: {}, note: "" }, '"v1"')).rejects.toBeInstanceOf(
      ConflictError,
    );
    try {
      await client.publish({ game: "demo", env: "dev", key: "k", value: {}, note: "" }, '"v1"');
    } catch (err) {
      expect((err as ConflictError).currentVersion).toBe(7);
    }
  });

  it("keeps the status on other failures, and reads the message the server sent", async () => {
    const client = new Client("view-token", "", responder(403, JSON.stringify({ error: 'role "viewer" does not hold config:publish' })));
    await expect(client.publish({ game: "demo", env: "dev", key: "k", value: {}, note: "" })).rejects.toThrow(/does not hold/);
    try {
      await client.publish({ game: "demo", env: "dev", key: "k", value: {}, note: "" });
    } catch (err) {
      expect(err).toBeInstanceOf(ApiError);
      expect((err as ApiError).status).toBe(403);
    }
  });

  // This is the test the browser asked for. Driving the real panel produced
  // "Illegal invocation" because the default fetch was called through `this`; a fake
  // fetch cannot detect that, so this test replaces the global and uses the real
  // constructor path.
  it("the default fetch works when it is called as a method of the client", async () => {
    const original = globalThis.fetch;
    const calls: string[] = [];
    globalThis.fetch = (async (url: RequestInfo | URL) => {
      calls.push(String(url));
      return new Response(JSON.stringify({ actor: "em", role: "admin", permissions: [], envs: [] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }) as typeof fetch;
    try {
      const client = new Client("op-token");
      const { data } = await client.whoami();
      expect(data.actor).toBe("em");
      expect(calls).toEqual(["/v1/whoami"]);
    } finally {
      globalThis.fetch = original;
    }
  });

  it("survives a body that is not JSON", async () => {
    const client = new Client("op-token", "", responder(500, "<html>gateway</html>"));
    await expect(client.whoami()).rejects.toThrow(/HTTP 500/);
  });
});
