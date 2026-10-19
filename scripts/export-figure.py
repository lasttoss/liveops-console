#!/usr/bin/env python3
"""Export a figure's HTML to the .png that the READMEs embed.

The HTML is the source; the PNG is what people actually look at, and it is the artefact nobody reads a
diff of. So it is exported the same way every time - a headless Chromium at deviceScaleFactor 2, the
width the figure lays itself out at - and `scripts/diagram-freshness.py` fails when the committed PNG
is older than the HTML it came from.

Playwright is not a dependency of building or testing this repository, so a missing playwright is not
an error here: it says so and exits 0, and the freshness check still compares what is committed.

usage: scripts/export-figure.py docs/figures/<name>.html
"""
import os
import sys


def main():
    if len(sys.argv) != 2:
        print(__doc__.strip().splitlines()[-1])
        return 2
    html = sys.argv[1]
    out = os.path.splitext(html)[0] + ".png"
    try:
        from playwright.sync_api import sync_playwright
    except ImportError:
        print("  export skipped: playwright is not installed")
        print("    pip install playwright && playwright install chromium")
        return 0
    try:
        with sync_playwright() as p:
            browser = p.chromium.launch()
            page = browser.new_page(viewport={"width": 1600, "height": 1200}, device_scale_factor=2)
            page.goto("file://" + os.path.abspath(html))
            page.wait_for_load_state("networkidle")
            page.wait_for_timeout(500)
            page.query_selector("svg").screenshot(path=out)
            browser.close()
    except Exception as error:  # a browser that is not installed is not a broken figure
        print(f"  export skipped: {error}")
        return 0
    print(f"  wrote {out}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
