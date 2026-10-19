#!/usr/bin/env python3
"""Check the commands this repository's README tells people to run.

Two documented commands that do not work were found by hand in sibling repositories: five `make`
targets the README named had been deleted from a Makefile, and another target died in a flag parser
because a variable it expanded was declared nowhere. Both were invisible to CI, because CI runs a
fixed list of targets rather than the commands the documentation promises.

So this does two things:

  1. every `make <target>`, `./scripts/...` and `./bin/...` the README names inside a code block or
     inline code span has to resolve - the target exists, the script exists and is executable, the
     binary is produced by `make build`;
  2. a per-repository list of those commands is actually run, because "the target exists" is not the
     same as "the target works".

Nothing in a Makefile recipe may need this machine: a `~/` path in a recipe is a command that works
for its author and nobody else.

README_RUN     ';'-separated commands to run. A leading '!' means the command must fail.
README_SKIP    ';'-separated `command | reason` pairs, printed rather than run, with the reason.

usage: README_RUN='...' README_SKIP='...' scripts/readme-check.py [repo-root]
"""
import os
import re
import subprocess
import sys
import time

FENCE = re.compile(r"```[a-zA-Z]*\n(.*?)```", re.S)
INLINE = re.compile(r"`([^`\n]+)`")
MAKE_CMD = re.compile(r"\bmake\s+([a-z][a-z0-9-]*)")
PATH_CMD = re.compile(r"(?<![\w/])\./((?:[A-Za-z0-9._-]+/)*[A-Za-z0-9._-]+?\.(?:sh|py))")
BIN_CMD = re.compile(r"(?<![\w/])\./(bin/[A-Za-z0-9._-]+)")
TARGET = re.compile(r"^([a-zA-Z0-9_-]+):", re.M)
MACHINE_PATH = re.compile(r"(~/\S*|\$HOME/\S*|\$\{HOME\}/\S*|/home/[a-z0-9_-]+/)")


def say(line=""):
    print(line, flush=True)


def documented_commands(root):
    text = open(os.path.join(root, "README.md"), encoding="utf-8").read()
    spans = FENCE.findall(text) + INLINE.findall(text)
    found = {}
    for body in spans:
        for lineno_hint in [body]:
            for target in MAKE_CMD.findall(lineno_hint):
                found.setdefault("make " + target, "make")
            for path in PATH_CMD.findall(lineno_hint):
                found.setdefault("./" + path, "path")
            for path in BIN_CMD.findall(lineno_hint):
                found.setdefault("./" + path, "bin")
    return found


def main():
    root = sys.argv[1] if len(sys.argv) > 1 else "."
    readme = os.path.join(root, "README.md")
    makefile_path = os.path.join(root, "Makefile")
    problems, checked = [], 0

    if not os.path.exists(readme):
        say("  no README.md: nothing to check")
        return 0

    makefile = open(makefile_path, encoding="utf-8").read() if os.path.exists(makefile_path) else ""
    targets = set(TARGET.findall(makefile))

    say("  1. every command the README names resolves")
    found = documented_commands(root)
    for command, kind in sorted(found.items()):
        if kind == "make":
            name = command.split()[1]
            if name in targets:
                say(f"    ok        {command}")
            else:
                problems.append(f"README names `{command}`, and the Makefile defines no such target")
                say(f"    BROKEN    {command}  - no such target")
                continue
            checked += 1
        elif kind == "path":
            full = os.path.join(root, command)
            if not os.path.exists(full):
                problems.append(f"README names `{command}`, which does not exist")
                say(f"    BROKEN    {command}  - missing")
            elif not os.access(full, os.X_OK):
                problems.append(f"README names `{command}`, which is not executable")
                say(f"    BROKEN    {command}  - not executable")
            else:
                say(f"    ok        {command}")
                checked += 1

    if "build" in targets:
        say("    running `make build`, to check the binaries the README names")
        build = subprocess.run(["make", "build"], cwd=root, capture_output=True, text=True)
        if build.returncode != 0:
            problems.append("`make build` failed, so no binary the README names can be checked")
            for line in (build.stdout + build.stderr).strip().splitlines()[-6:]:
                say("      " + line)
        else:
            for command, kind in sorted(found.items()):
                if kind != "bin":
                    continue
                if os.path.exists(os.path.join(root, command)):
                    say(f"    ok        {command}")
                    checked += 1
                else:
                    problems.append(f"README names `{command}`, which `make build` does not produce")
                    say(f"    BROKEN    {command}  - not built")

    say("  2. no target needs this machine")
    for lineno, line in enumerate(makefile.splitlines(), 1):
        # Recipe lines only, and not the comments inside them: a note about the checker is not a
        # dependency on the machine it was written on.
        if line.startswith("\t") and not line.lstrip().startswith("#") and MACHINE_PATH.search(line):
            problems.append(
                f"Makefile:{lineno} needs a path outside the repository: {MACHINE_PATH.search(line).group(1)}"
            )
            say(f"    BROKEN    Makefile:{lineno}  {line.strip()[:80]}")
    if not any("outside the repository" in p for p in problems):
        say("    ok        no recipe reaches outside the repository")

    skipped = [s for s in os.environ.get("README_SKIP", "").split(";") if s.strip()]
    if skipped:
        say("  3. not run here, with the reason")
        for entry in skipped:
            command, _, reason = entry.partition("|")
            say(f"    skipped   {command.strip()}  - {reason.strip()}")

    run = [s for s in os.environ.get("README_RUN", "").split(";") if s.strip()]
    if run:
        say("  4. the documented commands that can run here")
        for entry in run:
            entry = entry.strip()
            must_fail = entry.startswith("!")
            command = entry[1:].strip() if must_fail else entry
            started = time.time()
            try:
                done = subprocess.run(command, cwd=root, shell=True, capture_output=True, text=True, timeout=900)
                code, output = done.returncode, done.stdout + done.stderr
            except subprocess.TimeoutExpired:
                code, output = None, "timed out after 900s"
            took = time.time() - started
            # 127 is "no such command", which is never what the README promised, and a command that
            # cannot run is not a command that failed.
            good = code not in (None, 127) and ((code != 0) if must_fail else (code == 0))
            want = "must fail, " if must_fail else ""
            if good:
                say(f"    ok        {command}  ({want}exit {code}, {took:.1f}s)")
            else:
                problems.append(f"`{command}` did not do what the README says ({want}exit {code})")
                say(f"    BROKEN    {command}  ({want}exit {code}, {took:.1f}s)")
                for line in output.strip().splitlines()[-10:]:
                    say("      " + line)

    say()
    if problems:
        say(f"  {len(problems)} documented command(s) do not do what the README says:")
        for problem in problems:
            say(f"    - {problem}")
        say()
        say("  A command in the README is a promise. Either the command comes back, or the README stops")
        say("  making it.")
        return 1
    say(f"  {checked} documented command(s) checked, all of them resolve and behave.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
