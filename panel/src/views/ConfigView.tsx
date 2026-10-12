import { useCallback, useEffect, useState } from "react";
import { ApiError, Client, ConflictError, type Revision, type WhoAmI } from "../api";
import { canWrite, whyRefused } from "../policy";

type Props = { client: Client; me: WhoAmI | null; game: string; env: string };

export default function ConfigView({ client, me, game, env }: Props) {
  const [key, setKey] = useState("feature.double_xp");
  const [current, setCurrent] = useState<Revision | null>(null);
  const [etag, setEtag] = useState<string | null>(null);
  const [revisions, setRevisions] = useState<Revision[]>([]);
  const [draft, setDraft] = useState('{\n  "enabled": true,\n  "multiplier": 2\n}');
  const [note, setNote] = useState("");
  const [status, setStatus] = useState<string | null>(null);
  const [failure, setFailure] = useState<string | null>(null);
  const [conflict, setConflict] = useState<ConflictError | null>(null);
  const [baseVersion, setBaseVersion] = useState<number | null>(null);
  const [loading, setLoading] = useState(false);

  const load = useCallback(async () => {
    setFailure(null);
    setLoading(true);
    try {
      const revs = await client.revisions(game, env, key);
      setRevisions(revs.data.revisions);
      const latest = revs.data.revisions[0];
      if (!latest) {
        setCurrent(null);
        setEtag(null);
        setBaseVersion(null);
        setStatus(`no revision of ${key} in ${game}/${env} yet`);
        return;
      }
      setCurrent(latest);
      setEtag(`"v${latest.version}"`);
      setBaseVersion(latest.version);
      setStatus(`loaded v${latest.version} (ETag ${`"v${latest.version}"`})`);
    } catch (err) {
      setFailure(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [client, game, env, key]);

  useEffect(() => {
    void load();
  }, [load]);

  async function publish() {
    setFailure(null);
    setStatus(null);
    let value: unknown;
    try {
      value = JSON.parse(draft);
    } catch {
      setFailure("the value box is not valid JSON");
      return;
    }
    try {
      const { data } = await client.publish({ game, env, key, value, note }, etag ?? undefined);
      setConflict(null);
      setStatus(`published v${data.version} on top of ${baseVersion === null ? "nothing" : `v${baseVersion}`}`);
      setNote("");
      await load();
    } catch (err) {
      if (err instanceof ConflictError) {
        setConflict(err);
        setStatus(null);
        return;
      }
      setFailure(err instanceof ApiError ? `${err.status}: ${err.message}` : String(err));
    }
  }

  async function rollback(version: number) {
    setFailure(null);
    setConflict(null);
    try {
      const { data } = await client.rollback({ game, env, key, to_version: version, note: `rollback to v${version}` });
      setStatus(`rolled back: v${version}'s payload is now v${data.version}, and v${version} is still readable`);
      await load();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : String(err));
    }
  }

  const writable = me ? canWrite(me.role, "config:publish", env) : false;
  const refusal = me ? whyRefused(me.role, "config:publish", env) : "no token";

  return (
    <section className="grid">
      <div className="col">
        <h2>{writable ? "Read and edit" : "Read only"}</h2>
        <label className="field">
          key
          <input value={key} onChange={(e) => setKey(e.target.value)} />
        </label>

        <div className="box">
          <div className="box-head">
            <strong>{current ? `v${current.version}` : "no revision"}</strong>
            <span className="muted">{etag ?? "no ETag"}</span>
          </div>
          <pre>{current ? JSON.stringify(current.value, null, 2) : "—"}</pre>
          <p className="muted">
            {current ? `written by ${current.created_by} · ${new Date(current.created_at).toLocaleString()}` : "publish something to start the history"}
          </p>
        </div>

        <label className="field">
          new value (JSON)
          <textarea rows={7} value={draft} onChange={(e) => setDraft(e.target.value)} />
        </label>
        <label className="field">
          note
          <input value={note} onChange={(e) => setNote(e.target.value)} placeholder="why this change" />
        </label>
        <div className="row">
          <button onClick={() => void publish()} disabled={!writable} title={refusal ?? ""}>
            publish {baseVersion === null ? "as v1" : `on top of v${baseVersion}`}
          </button>
          <button className="ghost" onClick={() => void load()}>
            reload
          </button>
        </div>
        {!writable && <p className="warn">{refusal}</p>}
        {status && <p className="ok">{status}</p>}
        {failure && <p className="bad">{failure}</p>}

        {conflict && (
          <div className="conflict">
            <strong>Someone else got there first.</strong>
            <p>
              {conflict.message}
              {conflict.current ? ` — published by ${conflict.current.created_by}` : ""}. Your value was not written, and
              the version you were editing is still readable.
            </p>
            <div className="row">
              <button
                className="ghost"
                onClick={() => {
                  if (conflict.current) {
                    setDraft(JSON.stringify(conflict.current.value, null, 2));
                    setBaseVersion(conflict.current.version);
                    setEtag(`"v${conflict.current.version}"`);
                  }
                  setConflict(null);
                }}
              >
                use their value as my base
              </button>
              <button className="ghost" onClick={() => setConflict(null)}>
                dismiss
              </button>
            </div>
          </div>
        )}
      </div>

      <div className="col">
        <h2>History</h2>
        <p className="muted">Nothing here is edited in place: a rollback appends the old payload as a new revision.</p>
        <div className="table-wrap">
          <table className="history">
            <caption className="sr-only">
              revisions of {key} in {game}/{env}
            </caption>
            <thead>
              <tr>
                <th scope="col">version</th>
                <th scope="col">value</th>
                <th scope="col">note</th>
                <th scope="col">action</th>
              </tr>
            </thead>
            <tbody>
              {revisions.map((rev) => (
                <tr key={rev.id} className={current && rev.version === current.version ? "is-current" : ""}>
                  <td data-label="version" className="version">
                    v{rev.version}
                    {current && rev.version === current.version ? <span className="muted"> current</span> : null}
                  </td>
                  <td data-label="value">
                    <code>{JSON.stringify(rev.value)}</code>
                  </td>
                  <td data-label="note" className="muted">{rev.note || "—"}</td>
                  <td data-label="action">
                    {rev.version !== current?.version && (
                      <button
                        className="ghost"
                        disabled={!me || !canWrite(me.role, "config:rollback", env)}
                        aria-label={`rollback ${key} to v${rev.version}`}
                        title={`rollback appends v${rev.version}'s payload as a new revision`}
                        onClick={() => void rollback(rev.version)}
                      >
                        rollback
                      </button>
                    )}
                  </td>
                </tr>
              ))}
              {loading && revisions.length === 0 && (
                <tr>
                  <td colSpan={4}>
                    <span className="skeleton" />
                    <span className="skeleton" />
                  </td>
                </tr>
              )}
              {!loading && revisions.length === 0 && (
                <tr>
                  <td colSpan={4} className="muted">
                    no revisions yet — publish a value on the left to start the history
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </section>
  );
}
