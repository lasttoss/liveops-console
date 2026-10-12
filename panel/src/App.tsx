import { useEffect, useMemo, useState } from "react";
import { Client, type WhoAmI } from "./api";
import ConfigView from "./views/ConfigView";
import EventsView from "./views/EventsView";
import AuditView from "./views/AuditView";

// The token lives in localStorage and is typed in by hand. That is the honest choice
// for a demo with no identity provider behind it: no build-time secret, no token in the
// bundle, and the panel proves the same bearer-token path the API tests cover.
const TOKEN_KEY = "liveops.token";
const ENVS = ["dev", "staging", "prod"];
const GAMES = ["demo"];

type Tab = "config" | "events" | "audit";

export default function App() {
  const [token, setToken] = useState(() => window.localStorage.getItem(TOKEN_KEY) ?? "");
  const [me, setMe] = useState<WhoAmI | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>("config");
  const [env, setEnv] = useState("dev");
  const [game, setGame] = useState(GAMES[0] ?? "demo");

  const client = useMemo(() => new Client(token), [token]);

  useEffect(() => {
    window.localStorage.setItem(TOKEN_KEY, token);
    if (!token) {
      setMe(null);
      setError(null);
      return;
    }
    let cancelled = false;
    client
      .whoami()
      .then(({ data }) => {
        if (!cancelled) {
          setMe(data);
          setError(null);
        }
      })
      .catch((err: Error) => {
        if (!cancelled) {
          setMe(null);
          setError(err.message);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [client, token]);

  return (
    <div className="shell">
      <header>
        <h1>LiveOps console</h1>
        <p className="sub">
          Versioned configuration, scheduled events and an audit trail that cannot be edited. Every write sends
          <code> If-Match</code>, so two people on one key see a conflict instead of losing a change.
        </p>
        <div className="controls">
          <label className={error ? "invalid" : ""}>
            token
            <input
              type="password"
              value={token}
              placeholder="admin-token · op-token · view-token"
              onChange={(e) => setToken(e.target.value.trim())}
              aria-label="bearer token"
              aria-invalid={error ? true : undefined}
              aria-describedby={error ? "token-error" : undefined}
            />
          </label>
          <label>
            game
            <select value={game} onChange={(e) => setGame(e.target.value)}>
              {GAMES.map((g) => (
                <option key={g} value={g}>
                  {g}
                </option>
              ))}
            </select>
          </label>
          <label>
            env
            <select value={env} onChange={(e) => setEnv(e.target.value)}>
              {ENVS.map((e) => (
                <option key={e} value={e}>
                  {e}
                </option>
              ))}
            </select>
          </label>
          <div className="whoami" role="status">
            {me ? (
              <>
                <span className="pill">{me.actor}</span>
                <span className={"pill role-" + me.role}>{me.role}</span>
                <span className="muted">{me.permissions.length} permissions</span>
              </>
            ) : (
              <span className="muted">{error ? "not signed in" : "paste a token to begin — make demo prints one"}</span>
            )}
          </div>
        </div>
        {error && (
          <p id="token-error" className="err" role="alert">
            {error} — the token box above holds what you pasted; check it or paste another.
          </p>
        )}
      </header>

      <nav>
        {(["config", "events", "audit"] as Tab[]).map((t) => (
          <button key={t} className={tab === t ? "active" : ""} onClick={() => setTab(t)}>
            {t}
          </button>
        ))}
      </nav>

      <main>
        {tab === "config" && <ConfigView client={client} me={me} game={game} env={env} />}
        {tab === "events" && <EventsView client={client} me={me} game={game} env={env} />}
        {tab === "audit" && <AuditView client={client} me={me} />}
      </main>
    </div>
  );
}
