// A typed client for the API in this repository. Two things it does deliberately:
//
//  * it sends `If-Match` on every write, so two people editing one key is a visible
//    conflict rather than a lost change;
//  * it turns `409` into a ConflictError that carries the version that won, because a
//    panel that only knows "that failed" cannot show a diff.

export type Revision = {
  id: number;
  game: string;
  env: string;
  key: string;
  value: unknown;
  version: number;
  note: string;
  created_by: string;
  created_at: string;
};

export type WhoAmI = { actor: string; role: string; permissions: string[]; envs: string[] };

export type EventRow = {
  id: number;
  game: string;
  env: string;
  name: string;
  starts_at: string;
  ends_at: string;
  repeat_every_secs?: number;
  payload: unknown;
  state: string;
  created_by: string;
};

export type Fire = {
  event_id: number;
  fire_at: string;
  status: string;
  attempts: number;
  effect_count: number;
};

export type AuditEntry = {
  id: number;
  at: string;
  actor: string;
  action: string;
  target: string;
  outcome: string;
};

export class ApiError extends Error {
  constructor(readonly status: number, message: string) {
    super(message);
    this.name = "ApiError";
  }
}

export class ConflictError extends Error {
  constructor(
    readonly currentVersion: number | null,
    readonly current: Revision | null,
    message: string,
  ) {
    super(message);
    this.name = "ConflictError";
  }
}

export class Client {
  constructor(
    private readonly token: string,
    private readonly base = "",
    // Bound on purpose. `this.fetchImpl(...)` would otherwise call fetch with the Client
    // as its receiver, and the browser answers with "Illegal invocation" - a bug the
    // injected-fake tests could not see, because a fake does not check its receiver.
    // The test below sets globalThis.fetch to prove the default path is callable.
    private readonly fetchImpl: typeof fetch = fetch.bind(globalThis),
  ) {}

  private async call<T>(method: string, path: string, body?: unknown, etag?: string): Promise<{ data: T; etag: string | null }> {
    const headers: Record<string, string> = {};
    if (this.token) headers["Authorization"] = `Bearer ${this.token}`;
    if (body !== undefined) headers["Content-Type"] = "application/json";
    if (etag) headers["If-Match"] = etag;

    const resp = await this.fetchImpl(this.base + path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    const text = await resp.text();

    if (resp.status === 409) {
      let parsed: { current_version?: number; current?: Revision; error?: string } = {};
      try {
        parsed = text ? JSON.parse(text) : {};
      } catch {
        // A body we cannot read is still a conflict; the version is simply unknown.
      }
      throw new ConflictError(parsed.current_version ?? null, parsed.current ?? null, parsed.error ?? "version conflict");
    }
    if (!resp.ok) {
      let message = `HTTP ${resp.status}`;
      try {
        message = (JSON.parse(text) as { error?: string }).error ?? message;
      } catch {
        // keep the status when the body is not JSON
      }
      throw new ApiError(resp.status, message);
    }
    return { data: (text ? JSON.parse(text) : null) as T, etag: resp.headers.get("ETag") };
  }

  whoami() {
    return this.call<WhoAmI>("GET", "/v1/whoami");
  }

  // Reads go through `revisions` rather than a single GET: the panel always wants the
  // history next to the current value, and one request for both is one round trip.
  revisions(game: string, env: string, key: string) {
    const q = new URLSearchParams({ game, env, key });
    return this.call<{ revisions: Revision[] }>("GET", `/v1/config/revisions?${q}`);
  }

  publish(input: { game: string; env: string; key: string; value: unknown; note: string }, etag?: string) {
    return this.call<Revision>("PUT", "/v1/config", input, etag);
  }

  rollback(input: { game: string; env: string; key: string; to_version: number; note: string }) {
    return this.call<Revision>("POST", "/v1/config/rollback", input);
  }

  events(game: string, env: string) {
    const q = new URLSearchParams({ game, env });
    return this.call<{ events: EventRow[] }>("GET", `/v1/events?${q}`);
  }

  fires(eventID: number) {
    return this.call<{ fires: Fire[]; effects: number }>("GET", `/v1/events/${eventID}/fires`);
  }

  createEvent(input: Record<string, unknown>) {
    return this.call<EventRow>("POST", "/v1/events", input);
  }

  audit(target: string, beforeID?: number) {
    const q = new URLSearchParams();
    if (target) q.set("target", target);
    if (beforeID) q.set("before_id", String(beforeID));
    q.set("limit", "25");
    return this.call<{ entries: AuditEntry[] }>("GET", `/v1/audit?${q}`);
  }
}
