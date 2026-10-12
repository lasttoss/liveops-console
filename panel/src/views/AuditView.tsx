import { useCallback, useEffect, useState } from "react";
import { Client, type AuditEntry, type WhoAmI } from "../api";

type Props = { client: Client; me: WhoAmI | null };

export default function AuditView({ client, me }: Props) {
  const [entries, setEntries] = useState<AuditEntry[]>([]);
  const [target, setTarget] = useState("");
  const [failure, setFailure] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const load = useCallback(
    async (beforeID?: number) => {
      setLoading(true);
      try {
        const { data } = await client.audit(target, beforeID);
        setEntries((prev) => (beforeID ? [...prev, ...data.entries] : data.entries));
        setFailure(null);
      } catch (err) {
        setFailure(err instanceof Error ? err.message : String(err));
      } finally {
        setLoading(false);
      }
    },
    [client, target],
  );

  useEffect(() => {
    void load();
  }, [load]);

  if (!me) return <p className="muted">paste a token: the trail is readable by every role</p>;

  return (
    <section>
      <h2>Audit trail</h2>
      <p className="muted">
        Append-only: a trigger refuses <code>UPDATE</code>, <code>DELETE</code> and <code>TRUNCATE</code>, and an
        integration test asserts that it does. Refusals are written here too - "who tried and could not" is the question
        asked after an incident.
      </p>
      <div className="row">
        <input
          value={target}
          onChange={(e) => setTarget(e.target.value)}
          placeholder="filter by target, e.g. config:demo/dev/feature.double_xp"
          aria-label="target filter"
        />
        <button className="ghost" onClick={() => void load()}>
          filter
        </button>
      </div>
      {failure && <p className="bad">{failure}</p>}
      <table>
        <thead>
          <tr>
            <th>when</th>
            <th>actor</th>
            <th>action</th>
            <th>target</th>
            <th>outcome</th>
          </tr>
        </thead>
        <tbody>
          {entries.map((e) => (
            <tr key={e.id} className={e.outcome === "ok" ? "" : "is-refused"}>
              <td className="muted">{new Date(e.at).toLocaleString()}</td>
              <td>{e.actor}</td>
              <td>
                <code>{e.action}</code>
              </td>
              <td className="muted">{e.target}</td>
              <td>
                <span className={"outcome outcome-" + e.outcome}>{e.outcome}</span>
              </td>
            </tr>
          ))}
          {entries.length === 0 && (
            <tr>
              <td colSpan={5} className="muted">
                nothing recorded yet
              </td>
            </tr>
          )}
        </tbody>
      </table>
      <div className="row">
        <button
          className="ghost"
          disabled={loading || entries.length === 0}
          onClick={() => {
            const last = entries[entries.length - 1];
            if (last) void load(last.id);
          }}
        >
          load older
        </button>
      </div>
    </section>
  );
}
