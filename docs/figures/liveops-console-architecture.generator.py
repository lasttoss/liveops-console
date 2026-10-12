#!/usr/bin/env python3
"""Generate docs/figures/liveops-console-architecture.html.

Generated rather than hand-drawn, and measured rather than positioned by eye: every box's
width and height come from its own text, so editing a line below cannot overlap anything.
The first version of this file guessed coordinates and the export came back with the API's
command lines printed across the database column, which is what a picture is not for.

    python3 docs/figures/liveops-console-architecture.generator.py
    python3 <skill>/scripts/self_check.py docs/figures/liveops-console-architecture.html
    make diagram
"""
import html
import pathlib

PAPER, INK, MUTED, ACCENT, LINE, TINT = "#f5f5f5", "#2d3142", "#4f5d75", "#eb6c36", "#c9ccd1", "#fdf4ef"
FONT = {"sans": "'Geist', system-ui, sans-serif",
        "mono": "'Geist Mono', ui-monospace, monospace",
        "serif": "'Instrument Serif', serif"}
# Advance widths per character as a fraction of font size. Measured from the real webfont
# at these sizes; close enough that no line can run past its box.
ADVANCE = {"mono": 0.605, "sans": 0.525, "sans-bold": 0.565}
PAD = 16
GAP = 150  # wide enough for an arrow label, which is why it is not a round number

def esc(s):
    return html.escape(str(s))

def width(s, size, family="sans", weight="400"):
    key = "mono" if family == "mono" else ("sans-bold" if weight == "600" else "sans")
    return len(s) * ADVANCE[key] * size

def text(x, y, s, size=12.5, weight="400", fill=INK, family="sans", anchor="start"):
    return (f'<text x="{x:.0f}" y="{y:.0f}" font-family="{FONT[family]}" font-size="{size}" '
            f'font-weight="{weight}" fill="{fill}" text-anchor="{anchor}">{esc(s)}</text>')

def rect(x, y, w, h, fill="#ffffff", stroke=LINE, rx=10):
    return f'<rect x="{x:.0f}" y="{y:.0f}" width="{w:.0f}" height="{h:.0f}" rx="{rx}" fill="{fill}" stroke="{stroke}"/>'

def arrow(x1, y1, x2, y2, label="", dashed=False):
    dash = ' stroke-dasharray="5 4"' if dashed else ""
    out = [f'<line x1="{x1:.0f}" y1="{y1:.0f}" x2="{x2:.0f}" y2="{y2:.0f}" stroke="{MUTED}" stroke-width="1.6" '
           f'marker-end="url(#arrowhead)"{dash}/>']
    if label:
        mx, my = (x1 + x2) / 2, (y1 + y2) / 2
        lw = width(label, 11, "mono") + 12
        out.append(rect(mx - lw / 2, my - 12, lw, 20, fill=PAPER, stroke="none", rx=5))
        out.append(text(mx, my + 2, label, size=11, fill=MUTED, family="mono", anchor="middle"))
    return "".join(out)

class Block:
    """A titled card. Its size is the size of its contents, computed once, at build time."""

    def __init__(self, title, subtitle, lines, accent=INK):
        self.title, self.subtitle, self.accent = title, subtitle, lines and accent or accent
        self.lines = lines
        candidates = [width(title, 15, weight="600")]
        if subtitle:
            candidates.append(width(subtitle, 11.5, "mono"))
        for line, mono in lines:
            candidates.append(width(line, 11.5, "mono" if mono else "sans"))
        self.w = max(candidates) + PAD * 2
        self.h = PAD * 2 + 22 + (16 if subtitle else 0) + len(lines) * 17

    def render(self, x, y):
        out = [rect(x, y, self.w, self.h)]
        out.append(text(x + PAD, y + PAD + 12, self.title, size=15, weight="600", fill="%s" % INK))
        yy = y + PAD + 34
        if self.subtitle:
            out.append(text(x + PAD, yy + 4, self.subtitle, size=11.5, fill=MUTED, family="mono"))
            yy += 16
        for line, mono in self.lines:
            out.append(text(x + PAD, yy + 9, line, size=11.5, family="mono" if mono else "sans"))
            yy += 17
        return "".join(out)

CLIENTS = [
    Block("panel — React + TypeScript", "client", [
        ("If-Match on every write", True),
        ("conflict banner names the winner", True),
        ("renders the audit trail", True),
    ]),
    Block("demo client + CI — Go", "client", [
        ("make demo · make worker", True),
        ("greps its own output, so the claim cannot rot", True),
    ]),
]

API = Block("console-api", "--mode server | --mode worker", [
    ('PUT  /v1/config            If-Match: "v17" → 409 + winner', True),
    ("POST /v1/config/rollback   appends the old payload forward", True),
    ('GET  /v1/config            ETag: "v17"     → 304 on match', True),
    ("GET  /v1/events · /v1/audit · /metrics", True),
])
WORKER = Block("worker", "no queue: the due rows are the queue", [
    ("claim: UPDATE … FOR UPDATE SKIP LOCKED", True),
    ("effect + audit row, same transaction", True),
])

TABLES = [
    ("config_revisions", "UNIQUE (game, env, key, version)", "append-only: a rollback is an insert"),
    ("event_fires", "PRIMARY KEY (event_id, fire_at)", "one fire ⇒ one effect"),
    ("audit_log", "trigger: no UPDATE / DELETE / TRUNCATE", "refusals are recorded too"),
]
PROMISES = [
    "history is appended, never rewritten",
    "one fire produces one effect, by primary key",
    "a refusal lands in the same trail as a success",
]

# --- layout: three columns, each as wide as its own contents ---------------------
MARGIN, TOP = 28, 74
col_left_w = max(b.w for b in CLIENTS)
col_mid_w = max(API.w, WORKER.w)
tbl_w = max(width(n, 13.5, "mono") for n, _, _ in TABLES)
tbl_key_w = max(width(k, 11, "mono") for _, k, _ in TABLES)
tbl_why_w = max(width(w, 11) for _, _, w in TABLES)
col_right_w = max(tbl_w, tbl_key_w, tbl_why_w) + PAD * 2 + 28

x_left = MARGIN
x_mid = x_left + col_left_w + GAP
x_right = x_mid + col_mid_w + GAP
total_w = x_right + col_right_w + MARGIN

y = TOP
l1y = y + 16
l2y = l1y + CLIENTS[0].h + 34
api_y = y
worker_y = api_y + API.h + 52

tbl_h = 74
db_h = 52 + len(TABLES) * (tbl_h + 12) + 10
strip_y = max(worker_y + WORKER.h, y - 12 + db_h) + 46
strip_h = 78
db_y = y - 12

H = strip_y + strip_h + 46

svg = [f'<svg viewBox="0 0 {total_w:.0f} {H:.0f}" role="img" aria-labelledby="fig-title fig-desc" xmlns="http://www.w3.org/2000/svg">']
svg.append('<title id="fig-title">liveops-console architecture: the write path, the worker, and what PostgreSQL enforces</title>')
svg.append('<desc id="fig-desc">Two clients (the TypeScript panel and the Go demo client) call one Go binary. In server '
           'mode it takes a per-key advisory lock, appends a configuration revision and writes an audit row; in worker '
           'mode it claims due fires with FOR UPDATE SKIP LOCKED and records the effect in the same transaction. Three '
           'promises are enforced by the database rather than by the service: history is appended and never rewritten, '
           'one fire produces one effect because of a primary key, and the audit trail refuses updates.</desc>')
svg.append(f'<defs><marker id="arrowhead" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" '
           f'orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 z" fill="{MUTED}"/></marker></defs>')

svg.append(CLIENTS[0].render(x_left, l1y))
svg.append(CLIENTS[1].render(x_left, l2y))
svg.append(API.render(x_mid, api_y))
svg.append(WORKER.render(x_mid, worker_y))

svg.append(rect(x_right, db_y, col_right_w, db_h, fill="#fbfbfa"))
svg.append(text(x_right + PAD, db_y + 24, "PostgreSQL", size=15, weight="600"))
svg.append(text(x_right + PAD, db_y + 42, "the guarantees live here, not in the service", size=11.5, fill=MUTED, family="mono"))
ty = db_y + 54
for name, key, why in TABLES:
    svg.append(rect(x_right + 14, ty, col_right_w - 28, tbl_h))
    svg.append(text(x_right + 28, ty + 24, name, size=13.5, weight="600", family="mono"))
    svg.append(text(x_right + 28, ty + 44, key, size=11, fill=ACCENT, family="mono"))
    svg.append(text(x_right + 28, ty + 61, why, size=11, fill=MUTED))
    ty += tbl_h + 12

svg.append(arrow(x_left + col_left_w, l1y + 44, x_mid, l1y + 44, "token"))
svg.append(arrow(x_left + col_left_w, l2y + 40, x_mid, l2y + 40, "make demo"))
svg.append(arrow(x_mid + col_mid_w, api_y + 60, x_right, api_y + 60, "append"))
svg.append(arrow(x_right, api_y + 108, x_mid + col_mid_w, api_y + 108, "ETag", dashed=True))
svg.append(arrow(x_mid + col_mid_w, worker_y + 52, x_right, worker_y + 52, "claim"))

svg.append(rect(MARGIN, strip_y, total_w - MARGIN * 2, strip_h))
svg.append(text(MARGIN + PAD, strip_y + 26, "three promises, none of them enforced by the service's good behaviour",
                size=12.5, weight="600", fill=MUTED))
chip_w = (total_w - MARGIN * 2 - PAD * 2 - 24) / 3
for i, promise in enumerate(PROMISES):
    cx = MARGIN + PAD + i * (chip_w + 12)
    svg.append(rect(cx, strip_y + 38, chip_w, 28, fill=TINT, stroke="#e8cdbd", rx=6))
    svg.append(text(cx + 10, strip_y + 57, f"{i + 1}. {promise}", size=11.5))
svg.append(text(MARGIN, H - 16, "GET /healthz · /readyz · /metrics answer without a token: the NetworkPolicy is the "
                "access decision (on by default in charts/liveops-console).", size=11, fill=MUTED, family="mono"))
svg.append("</svg>")

meta = ('<!-- diagram-meta {"topic":"liveops-console-architecture","level":"container","type":"data-flow",'
        '"question":"How does a configuration change travel, and where is exactly-once actually enforced?",'
        '"sources":["internal/api/api.go","internal/pgstore/pgstore.go","internal/auth/rbac.go","internal/events/events.go",'
        '"internal/migrate/migrations/0001_schema.sql","internal/migrate/migrations/0002_audit_append_only.sql",'
        '"internal/metrics/metrics.go","cmd/console/main.go","panel/src/api.ts","charts/liveops-console/values.yaml"],'
        '"updated":"2026-10-12"} -->')

page = f"""{meta}
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>liveops-console architecture</title>
<link href="https://fonts.googleapis.com/css2?family=Instrument+Serif:ital@0;1&family=Geist:wght@400;500;600&family=Geist+Mono:wght@400;500;600&display=swap" rel="stylesheet">
<style>
  *, *::before, *::after {{ box-sizing: border-box; margin: 0; padding: 0; }}
  :root {{
    --color-paper: {PAPER}; --color-ink: {INK}; --color-muted: {MUTED}; --color-accent: {ACCENT};
    --font-sans: 'Geist', system-ui, sans-serif; --font-serif: 'Instrument Serif', serif;
    --font-mono: 'Geist Mono', ui-monospace, monospace;
  }}
  body {{ font-family: var(--font-sans); background: var(--color-paper); color: var(--color-ink);
         min-height: 100vh; display: flex; align-items: center; justify-content: center; padding: 3rem 2rem; }}
  .frame {{ max-width: {total_w + 40:.0f}px; width: 100%; }}
  .eyebrow {{ font-family: var(--font-mono); font-size: 0.66rem; font-weight: 500; letter-spacing: 0.18em;
              text-transform: uppercase; color: var(--color-muted); margin-bottom: 0.5rem; }}
  h1 {{ font-family: var(--font-serif); font-size: clamp(1.5rem, 2.4vw + 0.75rem, 2rem); font-weight: 400;
        letter-spacing: -0.02em; line-height: 1.15; color: var(--color-ink); margin-bottom: 1.5rem; }}
  .diagram-container {{ width: 100%; overflow-x: auto; }}
  svg {{ width: 100%; max-width: {total_w:.0f}px; display: block; }}
  .caption {{ font-family: var(--font-mono); font-size: 0.7rem; color: var(--color-muted); margin-top: 1rem; }}
</style>
</head>
<body>
<div class="frame">
  <p class="eyebrow">liveops-console · container view</p>
  <h1>A configuration change, from the button to the audit trail</h1>
  <div class="diagram-container">
{"".join(svg)}
  </div>
  <p class="caption">The database is where the promises are: an advisory lock serialises writes per key, a unique
  constraint numbers the versions, a primary key makes an effect happen once, and a trigger refuses to let the trail be
  edited. The service is allowed to be wrong; it is not allowed to be quietly wrong.</p>
</div>
</body>
</html>
"""
out = pathlib.Path(__file__).with_name("liveops-console-architecture.html")
out.write_text(page, encoding="utf-8")
print(f"wrote {out.name}: {total_w:.0f}x{H:.0f} viewBox, {len(page)} bytes")
