import { useCallback, useEffect, useState } from "react";
import { Client, type EventRow, type Fire, type WhoAmI } from "../api";
import { canWrite, whyRefused } from "../policy";

type Props = { client: Client; me: WhoAmI | null; game: string; env: string };

function localInput(date: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

export default function EventsView({ client, me, game, env }: Props) {
  const now = new Date();
  const [events, setEvents] = useState<EventRow[]>([]);
  const [fires, setFires] = useState<Record<number, { fires: Fire[]; effects: number }>>({});
  const [name, setName] = useState("weekly_quest");
  const [starts, setStarts] = useState(localInput(now));
  const [ends, setEnds] = useState(localInput(new Date(now.getTime() + 3600_000)));
  const [repeat, setRepeat] = useState("");
  const [payload, setPayload] = useState('{\n  "reward": "100 coins"\n}');
  const [status, setStatus] = useState<string | null>(null);
  const [failure, setFailure] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const { data } = await client.events(game, env);
      setEvents(data.events);
    } catch (err) {
      setFailure(err instanceof Error ? err.message : String(err));
    }
  }, [client, game, env]);

  useEffect(() => {
    void load();
  }, [load]);

  async function showFires(id: number) {
    const { data } = await client.fires(id);
    setFires((prev) => ({ ...prev, [id]: data }));
  }

  async function create() {
    setFailure(null);
    setStatus(null);
    let parsed: unknown = {};
    try {
      parsed = payload.trim() === "" ? {} : JSON.parse(payload);
    } catch {
      setFailure("the payload box is not valid JSON");
      return;
    }
    try {
      const { data } = await client.createEvent({
        game,
        env,
        name,
        starts_at: new Date(starts).toISOString(),
        ends_at: new Date(ends).toISOString(),
        repeat_every_secs: repeat.trim() === "" ? null : Number(repeat),
        payload: parsed,
      });
      setStatus(`event ${data.id} created; its fires exist now, not when they are due`);
      await load();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : String(err));
    }
  }

  const writable = me ? canWrite(me.role, "events:write", env) : false;

  return (
    <section className="grid">
      <div className="col">
        <h2>Schedule</h2>
        <label className="field">
          name
          <input value={name} onChange={(e) => setName(e.target.value)} />
        </label>
        <label className="field">
          starts
          <input type="datetime-local" value={starts} onChange={(e) => setStarts(e.target.value)} />
        </label>
        <label className="field">
          ends
          <input type="datetime-local" value={ends} onChange={(e) => setEnds(e.target.value)} />
        </label>
        <label className="field">
          repeat every (seconds, blank = once)
          <input value={repeat} onChange={(e) => setRepeat(e.target.value)} placeholder="60" />
        </label>
        <label className="field">
          payload (JSON)
          <textarea rows={5} value={payload} onChange={(e) => setPayload(e.target.value)} />
        </label>
        <div className="row">
          <button onClick={() => void create()} disabled={!writable} title={me ? whyRefused(me.role, "events:write", env) ?? "" : ""}>
            create event
          </button>
          <button className="ghost" onClick={() => void load()}>
            reload
          </button>
        </div>
        {!writable && <p className="warn">{me ? whyRefused(me.role, "events:write", env) : "no token"}</p>}
        {status && <p className="ok">{status}</p>}
        {failure && <p className="bad">{failure}</p>}
      </div>

      <div className="col">
        <h2>Events</h2>
        <div className="table-wrap">
          <table className="events">
            <caption className="sr-only">
              scheduled events for {game}/{env}
            </caption>
            <thead>
              <tr>
                <th scope="col">id</th>
                <th scope="col">name</th>
                <th scope="col">window</th>
                <th scope="col">every</th>
                <th scope="col">fires</th>
              </tr>
            </thead>
          <tbody>
            {events.map((ev) => (
              <tr key={ev.id}>
                <td data-label="id">{ev.id}</td>
                <td data-label="name">{ev.name}</td>
                <td data-label="window" className="muted">
                  {new Date(ev.starts_at).toLocaleString()} → {new Date(ev.ends_at).toLocaleString()}
                </td>
                <td data-label="every">{ev.repeat_every_secs ? `${ev.repeat_every_secs}s` : "once"}</td>
                <td data-label="fires">
                  <button className="ghost" onClick={() => void showFires(ev.id)}>
                    inspect
                  </button>
                  {fires[ev.id] && (
                    <span className="muted">
                      {" "}
                      {fires[ev.id]?.fires.length} fire(s), {fires[ev.id]?.effects} effect(s)
                    </span>
                  )}
                </td>
              </tr>
            ))}
            {events.length === 0 && (
              <tr>
                <td colSpan={5} className="muted">
                  nothing scheduled
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
