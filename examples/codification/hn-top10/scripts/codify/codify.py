"""Deterministic helpers for the agentic codification driver (plan 003).

Subcommands:
  resolve-paths      make the driver's paths absolute and find the markov binary
  begin-iteration    create the next iter-NNNN directory
  record-fixture     snapshot topstories.json and the first 10 items
  evidence           summarize a task run's events and collect its transcripts
  prepare-candidate  copy the project into a fresh candidate directory
  validate           check a candidate against the driver guard and a fixture
  promote            archive the current task files and install the candidate's
  rollback           install an archived version's task files
  record             append one row to metrics.jsonl
  spend              total codification spend so far

Each subcommand prints one JSON object on stdout.
"""

import argparse
import fnmatch
import functools
import glob
import http.server
import json
import os
import re
import shutil
import subprocess
import sys
import threading
import time
import urllib.request
from datetime import datetime

TOP_N = 10
OUTPUT_FILE = "hn-top10.md"
IGNORED = ("__pycache__", "*.db", "*.db-*", ".keep")


def emit(obj):
    json.dump(obj, sys.stdout, indent=2, sort_keys=True)
    sys.stdout.write("\n")


def fetch_json(url):
    req = urllib.request.Request(url, headers={"User-Agent": "markov-codify"})
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.load(resp)


def write_json(path, obj):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w") as f:
        json.dump(obj, f, indent=2, sort_keys=True)
        f.write("\n")


def read_json(path, default=None):
    try:
        with open(path) as f:
            return json.load(f)
    except (OSError, ValueError):
        return default


# The output contract shared by the task prompt and the validator.
def expected_lines(fixture):
    ids = read_json(os.path.join(fixture, "topstories.json"))[:TOP_N]
    lines = []
    for item_id in ids:
        item = read_json(os.path.join(fixture, "item", f"{item_id}.json"))
        url = item.get("url") or f"https://news.ycombinator.com/item?id={item_id}"
        lines.append(f"- [{item['title']}]({url})")
    return lines


def output_lines(path):
    with open(path) as f:
        return [line.rstrip() for line in f.read().strip().splitlines()]


def failure_text(proc):
    """The useful part of a failed markov command: its error lines, not the usage text."""
    out = (proc.stdout + proc.stderr).strip().splitlines()
    errors = [line for line in out if line.startswith("Error:") or " failed" in line or "failed:" in line]
    return "\n".join(errors or out[-20:])[-2000:]


def check_format(lines):
    pattern = re.compile(r"^- \[.+\]\(https?://\S+\)$")
    bad = [line for line in lines if not pattern.match(line)]
    if len(lines) != TOP_N:
        return f"expected {TOP_N} lines, got {len(lines)}"
    if bad:
        return f"line not in '- [title](url)' form: {bad[0]!r}"
    return None


def cmd_record_fixture(args):
    base = args.base_url.rstrip("/")
    ids = fetch_json(f"{base}/topstories.json")
    write_json(os.path.join(args.out, "topstories.json"), ids)
    for item_id in ids[:TOP_N]:
        write_json(os.path.join(args.out, "item", f"{item_id}.json"), fetch_json(f"{base}/item/{item_id}.json"))
    emit({"fixture": args.out, "ids": ids[:TOP_N]})


def load_events(path):
    events = []
    try:
        with open(path) as f:
            for line in f:
                line = line.strip()
                if line:
                    events.append(json.loads(line))
    except OSError:
        pass
    return events


def summarize(events):
    """Counts executed steps by kind and the agent's share of time and tokens."""
    agent = native = 0
    tokens = turns = 0
    cost = agent_seconds = 0.0
    sessions = []
    for ev in events:
        if ev.get("event_type") not in ("step_completed", "step_failed"):
            continue
        step_type = ev.get("step_type") or ""
        if not step_type or step_type == "workflow":
            continue
        out = ev.get("output") or {}
        if step_type == "claude":
            agent += 1
            tokens += int(out.get("tokens") or 0)
            turns += int(out.get("num_turns") or 0)
            cost += float(out.get("total_cost_usd") or 0)
            agent_seconds += float(ev.get("duration_seconds") or 0)
            if out.get("session_id"):
                sessions.append(out["session_id"])
        else:
            native += 1
    stamps = [ev["timestamp"] for ev in events if ev.get("timestamp")]
    wall = 0.0
    if len(stamps) >= 2:
        wall = _seconds(stamps[-1]) - _seconds(stamps[0])
    total = agent + native
    return {
        "agent_steps": agent,
        "native_steps": native,
        "coverage_steps": round(native / total, 3) if total else 0.0,
        "agent_seconds": round(agent_seconds, 1),
        "wall_seconds": round(wall, 1),
        "coverage_time": round(1 - agent_seconds / wall, 3) if wall > 0 else 0.0,
        "tokens": tokens,
        "turns": turns,
        "cost_usd": round(cost, 4),
        "sessions": sessions,
        "failed": any(ev.get("event_type") == "run_failed" for ev in events),
    }


def _seconds(stamp):
    return datetime.fromisoformat(stamp.replace("Z", "+00:00")).timestamp()


def in_run(event, run_id):
    """Whether an event belongs to run_id or one of its sub-runs (<run_id>-...)."""
    rid = event.get("run_id") or ""
    return rid == run_id or rid.startswith(run_id + "-")


def cmd_evidence(args):
    evidence = os.path.join(args.iteration_dir, "evidence")
    os.makedirs(os.path.join(evidence, "transcripts"), exist_ok=True)
    events = load_events(args.events)
    if args.run_id:
        # One event log holds the whole loop; keep this iteration's task run.
        events = [ev for ev in events if in_run(ev, args.run_id)]
    with open(os.path.join(evidence, "events.jsonl"), "w") as f:
        for ev in events:
            f.write(json.dumps(ev) + "\n")
    metrics = summarize(events)
    home = os.path.expanduser("~/.claude/projects")
    for sid in metrics["sessions"]:
        for path in glob.glob(os.path.join(home, "*", f"{sid}.jsonl")):
            shutil.copy(path, os.path.join(evidence, "transcripts", f"{sid}.jsonl"))
    iterations = sorted(glob.glob(os.path.join(os.path.dirname(os.path.abspath(args.iteration_dir)), "iter-*")))
    current = os.path.abspath(args.iteration_dir)
    if current in iterations and iterations.index(current) > 0:
        previous = iterations[iterations.index(current) - 1]
        for src, name in (("retro.json", "previous_retro.json"), ("validation/result.json", "previous_validation.json")):
            if os.path.exists(os.path.join(previous, src)):
                shutil.copy(os.path.join(previous, src), os.path.join(evidence, name))
    out = os.path.join(args.iteration_dir, "out", OUTPUT_FILE)
    metrics["output_format_error"] = check_format(output_lines(out)) if os.path.exists(out) else "no output file"
    write_json(os.path.join(evidence, "metrics.json"), metrics)
    emit(metrics)


def _ignore(directory, names):
    return shutil.ignore_patterns(*IGNORED)(directory, names)


def cmd_resolve_paths(args):
    markov = args.markov_bin
    if os.sep in markov:
        markov = os.path.abspath(markov)
    else:
        markov = shutil.which(markov) or os.environ.get("MARKOV_BIN") or ""
    if not markov:
        sys.exit(f"cannot find the markov binary {args.markov_bin!r}: put it on PATH, set MARKOV_BIN, or pass --var markov_bin=...")
    emit({
        "codify_dir": os.path.abspath(args.codify_dir),
        "project_dir": os.path.abspath(args.project_dir),
        "markov_docs": os.path.abspath(args.markov_docs),
        "markov_bin": markov,
    })


def cmd_begin_iteration(args):
    os.makedirs(args.codify_dir, exist_ok=True)
    n = len(glob.glob(os.path.join(args.codify_dir, "iter-*"))) + 1
    path = os.path.join(args.codify_dir, f"iter-{n:04d}")
    os.makedirs(path)
    emit({"iteration_dir": path, "n": n})


def cmd_spend(args):
    total = 0.0
    rows = 0
    try:
        with open(args.metrics_file) as f:
            for line in f:
                if line.strip():
                    rows += 1
                    total += (json.loads(line).get("retro") or {}).get("cost_usd") or 0
    except OSError:
        pass
    emit({"retro_cost_usd": round(total, 4), "iterations": rows})


def cmd_prepare_candidate(args):
    shutil.rmtree(args.out, ignore_errors=True)
    shutil.copytree(args.project, args.out, ignore=_ignore, symlinks=True)
    emit({"candidate": args.out})


def tree_files(root):
    files = {}
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d != "__pycache__"]
        for name in filenames:
            if any(fnmatch.fnmatch(name, pat) for pat in IGNORED):
                continue
            path = os.path.join(dirpath, name)
            with open(path, "rb") as f:
                files[os.path.relpath(path, root)] = f.read()
    return files


def task_file(rel):
    """The files a candidate may change: the task workflow, its helper
    workflows and its scripts."""
    if rel.startswith("scripts/task/"):
        return True
    return fnmatch.fnmatch(rel, "workflows/task.yaml") or fnmatch.fnmatch(rel, "workflows/task_*.yaml")


def changed_files(project, candidate):
    before, after = tree_files(project), tree_files(candidate)
    return sorted(rel for rel in before.keys() | after.keys() if before.get(rel) != after.get(rel))


class QuietHandler(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *args):
        pass


def cmd_validate(args):
    if os.sep in args.markov:
        args.markov = os.path.abspath(args.markov)  # the child run has its own working directory
    work = args.work
    shutil.rmtree(work, ignore_errors=True)
    os.makedirs(work)
    result = {"status": "failed", "reasons": [], "changed_files": [], "candidate": None}

    def done(status=None):
        if status:
            result["status"] = status
        write_json(os.path.join(work, "result.json"), result)
        emit(result)

    changed = changed_files(args.project, args.candidate)
    result["changed_files"] = changed
    protected = [rel for rel in changed if not task_file(rel)]
    if protected:
        result["reasons"].append(f"protected files changed: {', '.join(protected)}")
        return done()
    if not changed:
        return done("no_change")
    scripts = [rel for rel in changed if rel.startswith("scripts/task/") and os.path.exists(os.path.join(args.candidate, rel))]
    if scripts and not (read_json(args.retro, {}) or {}).get("script_justification"):
        result["reasons"].append(f"scripts added or changed without a script_justification in retro.json: {', '.join(scripts)}")
        return done()

    proc = subprocess.run([args.markov, "validate", args.candidate], capture_output=True, text=True)
    if proc.returncode != 0:
        result["reasons"].append(f"markov validate failed: {failure_text(proc)}")
        return done()

    handler = functools.partial(QuietHandler, directory=args.fixture)
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    out_dir = os.path.join(work, "out")
    events = os.path.join(work, "events.jsonl")
    started = time.monotonic()
    try:
        proc = subprocess.run(
            [
                args.markov, "run", args.candidate, "--workflow", "task",
                "--var", f"hn_base_url=http://127.0.0.1:{server.server_port}",
                "--var", f"out_dir={out_dir}",
                "--state-store", os.path.join(work, "state.db"),
                "--callback", f"jsonl://{events}",
            ],
            capture_output=True, text=True, timeout=args.timeout, cwd=work,
        )
    except subprocess.TimeoutExpired:
        result["reasons"].append(f"candidate run exceeded {args.timeout}s")
        return done()
    finally:
        server.shutdown()
    result["candidate"] = summarize(load_events(events))
    result["candidate"]["run_seconds"] = round(time.monotonic() - started, 1)
    if proc.returncode != 0:
        result["reasons"].append(f"candidate run failed: {failure_text(proc)}")
        return done()

    out = os.path.join(out_dir, OUTPUT_FILE)
    if not os.path.exists(out):
        result["reasons"].append(f"candidate wrote no {OUTPUT_FILE}")
        return done()
    got, want = output_lines(out), expected_lines(args.fixture)
    if got != want:
        for i in range(max(len(got), len(want))):
            g = got[i] if i < len(got) else "<missing>"
            w = want[i] if i < len(want) else "<extra>"
            if g != w:
                result["reasons"].append(f"line {i + 1}: got {g!r}, want {w!r}")
                break
        return done()
    done("passed")


def version_dirs(versions):
    if not os.path.isdir(versions):
        return []
    return sorted(d for d in os.listdir(versions) if re.fullmatch(r"v\d{4}", d))


def copy_task_files(src, dst):
    """Replaces dst's task files with src's (see task_file)."""
    workflows = os.path.join(dst, "workflows")
    os.makedirs(workflows, exist_ok=True)
    for path in glob.glob(os.path.join(workflows, "task_*.yaml")):
        os.remove(path)
    for path in [os.path.join(src, "workflows", "task.yaml")] + glob.glob(os.path.join(src, "workflows", "task_*.yaml")):
        shutil.copy(path, workflows)
    shutil.rmtree(os.path.join(dst, "scripts", "task"), ignore_errors=True)
    src_scripts = os.path.join(src, "scripts", "task")
    if os.path.isdir(src_scripts):
        shutil.copytree(src_scripts, os.path.join(dst, "scripts", "task"), ignore=_ignore)
    else:
        os.makedirs(os.path.join(dst, "scripts", "task"))


def archive(project, versions):
    existing = version_dirs(versions)
    name = f"v{len(existing):04d}"
    copy_task_files(project, os.path.join(versions, name))
    return name


def cmd_promote(args):
    if not version_dirs(args.versions):
        archive(args.project, args.versions)  # v0000: the baseline
    copy_task_files(args.candidate, args.project)
    version = archive(args.project, args.versions)
    promotion = {"version": version, "versions": args.versions}
    write_json(os.path.join(args.iteration_dir, "promotion.json"), promotion)
    emit(promotion)


def cmd_rollback(args):
    src = os.path.join(args.versions, args.version)
    if not os.path.isdir(src):
        sys.exit(f"no such version: {src}")
    copy_task_files(src, args.project)
    emit({"installed": args.version})


def cmd_record(args):
    it = args.iteration_dir
    validation = read_json(os.path.join(it, "validation", "result.json"), {})
    retro = read_json(os.path.join(it, "retro.json"), {})
    row = {
        "iteration": os.path.basename(it.rstrip("/")),
        "time": time.strftime("%Y-%m-%dT%H:%M:%S%z"),
        "task": read_json(os.path.join(it, "evidence", "metrics.json"), {}),
        "retro": {
            **retro,
            "cost_usd": _num(args.retro_cost),
            "tokens": _num(args.retro_tokens),
            "failed": args.retro_failed.lower() == "true",
        },
        "validation": {
            "status": validation.get("status"),
            "reasons": validation.get("reasons"),
            "changed_files": validation.get("changed_files"),
            "candidate": validation.get("candidate"),
        },
        "promoted_version": (read_json(os.path.join(it, "promotion.json"), {}) or {}).get("version"),
    }
    with open(args.metrics_file, "a") as f:
        f.write(json.dumps(row, sort_keys=True) + "\n")
    emit(row)


def _num(value):
    try:
        return float(value)
    except (TypeError, ValueError):
        return None


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="cmd", required=True)

    p = sub.add_parser("resolve-paths")
    p.add_argument("--codify-dir", required=True)
    p.add_argument("--project-dir", required=True)
    p.add_argument("--markov-docs", required=True)
    p.add_argument("--markov-bin", required=True)
    p.set_defaults(fn=cmd_resolve_paths)

    p = sub.add_parser("begin-iteration")
    p.add_argument("--codify-dir", required=True)
    p.set_defaults(fn=cmd_begin_iteration)

    p = sub.add_parser("spend")
    p.add_argument("--metrics-file", required=True)
    p.set_defaults(fn=cmd_spend)

    p = sub.add_parser("record-fixture")
    p.add_argument("--base-url", required=True)
    p.add_argument("--out", required=True)
    p.set_defaults(fn=cmd_record_fixture)

    p = sub.add_parser("evidence")
    p.add_argument("--events", required=True)
    p.add_argument("--iteration-dir", required=True)
    p.add_argument("--run-id", default="", help="keep only this run's and its sub-runs' events")
    p.set_defaults(fn=cmd_evidence)

    p = sub.add_parser("prepare-candidate")
    p.add_argument("--project", required=True)
    p.add_argument("--out", required=True)
    p.set_defaults(fn=cmd_prepare_candidate)

    p = sub.add_parser("validate")
    p.add_argument("--project", required=True)
    p.add_argument("--candidate", required=True)
    p.add_argument("--fixture", required=True)
    p.add_argument("--work", required=True)
    p.add_argument("--markov", default="markov")
    p.add_argument("--retro", required=True, help="the retro agent's retro.json")
    p.add_argument("--timeout", type=int, default=900)
    p.set_defaults(fn=cmd_validate)

    p = sub.add_parser("promote")
    p.add_argument("--project", required=True)
    p.add_argument("--candidate", required=True)
    p.add_argument("--versions", required=True)
    p.add_argument("--iteration-dir", required=True)
    p.set_defaults(fn=cmd_promote)

    p = sub.add_parser("rollback")
    p.add_argument("--project", required=True)
    p.add_argument("--versions", required=True)
    p.add_argument("--version", required=True)
    p.set_defaults(fn=cmd_rollback)

    p = sub.add_parser("record")
    p.add_argument("--iteration-dir", required=True)
    p.add_argument("--metrics-file", required=True)
    p.add_argument("--retro-cost", default="")
    p.add_argument("--retro-tokens", default="")
    p.add_argument("--retro-failed", default="false")
    p.set_defaults(fn=cmd_record)

    args = parser.parse_args()
    args.fn(args)


if __name__ == "__main__":
    main()
