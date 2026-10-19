#!/usr/bin/env python3
"""Check every figure in docs/figures against the sources the figure declares.

A figure that names its own sources in a `diagram-meta` comment can be checked mechanically: the
commit that last touched the figure has to be at least as new as the newest commit that touched any
of those sources. Without that check a figure drifts - it keeps describing a system that has changed,
and nothing in the repository says so.

Verdicts, one line per figure:

  FRESH              the figure is at least as new as everything it names
  STALE              a source was committed after the figure
  TOUCHED            a source has uncommitted changes, so the figure cannot be checked yet
  UNCOMMITTED        the figure itself is not committed yet
  SOURCES-UNTRACKED  a source has never been committed, so there is nothing to compare against
  NO-META            the figure does not name its sources
  EXPORT-STALE       the committed .png export is older than the .html it was made from

Exit code is non-zero when any figure is STALE, NO-META or EXPORT-STALE.

usage: scripts/diagram-freshness.py [repo-root]
"""
import json
import os
import re
import subprocess
import sys

META = re.compile(r"<!--\s*diagram-meta\s+(\{.*?\})\s*-->", re.S)


def git(*args, cwd):
    out = subprocess.run(("git",) + args, cwd=cwd, capture_output=True, text=True)
    return out.stdout.strip() if out.returncode == 0 else ""


def commit_time(path, root):
    stamp = git("log", "-1", "--format=%ct", "--", path, cwd=root)
    return int(stamp) if stamp.isdigit() else None


def dirty(path, root):
    return bool(git("status", "--porcelain", "--", path, cwd=root))


def main():
    root = sys.argv[1] if len(sys.argv) > 1 else "."
    figures = sorted(
        os.path.join(root, "docs", "figures", name)
        for name in os.listdir(os.path.join(root, "docs", "figures"))
        if name.endswith(".html") and name != "index.html"
    ) if os.path.isdir(os.path.join(root, "docs", "figures")) else []

    if not figures:
        print("  no figures in docs/figures: nothing to check")
        return 0

    failures = 0
    for figure in figures:
        rel = os.path.relpath(figure, root)
        text = open(figure, encoding="utf-8").read()
        find = META.search(text)
        if not find:
            print(f"  {'NO-META':<17} {rel}  - no diagram-meta comment naming its sources")
            failures += 1
            continue
        meta = json.loads(find.group(1))
        sources = meta.get("sources", [])
        question = meta.get("question", "")

        fig_time = commit_time(rel, root)
        if fig_time is None:
            verdict, detail = "UNCOMMITTED", "the figure has not been committed"
        elif any(dirty(s, root) for s in sources):
            verdict, detail = "TOUCHED", "a source is modified and not committed"
        else:
            times = {}
            for source in sources:
                stamp = commit_time(source, root)
                if stamp is not None:
                    times[source] = stamp
                elif not os.path.exists(os.path.join(root, source)):
                    times[source] = None
            missing = [s for s, t in times.items() if t is None]
            newest = max((t for t in times.values() if t is not None), default=None)
            if missing:
                verdict = "SOURCES-UNTRACKED"
                detail = "never committed: " + ", ".join(sorted(missing))
            elif newest is not None and newest > fig_time:
                late = sorted(s for s, t in times.items() if t == newest)
                verdict = "STALE"
                detail = "a source was committed after the figure: " + ", ".join(late)
            else:
                verdict, detail = "FRESH", ""

        export = figure[: -len(".html")] + ".png"
        if verdict == "FRESH" and os.path.exists(export) and fig_time is not None:
            export_time = commit_time(os.path.relpath(export, root), root)
            if export_time is not None and export_time < fig_time:
                # The HTML is current, the raster nobody reads the diff of is not: README embeds the
                # export, so the picture a reader sees can be older than the figure it came from.
                verdict = "EXPORT-STALE"
                detail = "the committed .png is older than the .html it was exported from"
        failed = verdict in ("STALE", "NO-META", "EXPORT-STALE")

        print(f"  {verdict:<17} {rel}" + (f"  - {detail}" if detail else ""))
        if question and failed:
            print(f"  {'':<17} {question}")
        if failed:
            failures += 1

    if failures:
        print()
        print("  A figure is only as true as the sources it names. Regenerate it, or narrow what it claims,")
        print("  and commit the figure in the same change as the source that moved it.")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
