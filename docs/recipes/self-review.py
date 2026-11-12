#!/usr/bin/env python3
"""Offline self-review recipe; requires Python 3.9+ and verdict on PATH."""
import argparse
import json
import os
from pathlib import Path
import signal
import subprocess
import time


class Stop(Exception):
    pass


def execute(argv, seconds, agent=False):
    with subprocess.Popen(argv, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                          start_new_session=(os.name != "nt")) as child:
        try:
            out, err = child.communicate(timeout=seconds)
        except subprocess.TimeoutExpired:
            if os.name != "nt":
                os.killpg(child.pid, signal.SIGKILL)
            else:
                child.kill()
            child.communicate()
            raise Stop("budget") from None
    if agent:
        if child.returncode:
            raise Stop("agent-failed")
        return None
    try:
        envelope = json.loads(out)
    except ValueError:
        raise Stop("invalid-envelope") from None
    if envelope.get("error"):
        raise Stop(envelope["error"]["code"])
    if child.returncode not in (0, 1):
        raise Stop("tool-failed")
    return envelope["data"]


def loop(repo, agent, rounds, budget, output, verdict="verdict", call=execute,
         clock=time.monotonic):
    output = Path(output)
    output.mkdir(parents=True, exist_ok=False)
    deadline = clock() + budget
    receipts = []
    previous = None

    def invoke(args, is_agent=False):
        remaining = deadline - clock()
        if remaining <= 0:
            raise Stop("budget")
        return call(args, remaining, is_agent)

    reason = "max-rounds"
    for number in range(1, rounds + 1):
        receipt = {"round": number, "findings": None, "gate": None, "cost": None}
        name = f"sr-{output.name[:12]}-{number}"
        local = "local-" + name
        bundle = Path(".verdict/state") / local
        try:
            invoke([verdict, "fetch-local", repo, name, "--json"])
            launch = invoke([verdict, "run", repo, local, "--agent", agent,
                             "--print", "--json"])
            argv = launch["argv"]
            argv[0] = launch["binary"]
            argv[-1] += ("\nSelf-review loop: attest coverage[] for every reviewed hunk. "
                         "Review the fetched snapshot and write review.json FIRST. "
                         "Then fix the open findings in the working tree only if you reported findings. A clean review must leave the tree unchanged. Do not commit. "
                         "Never change the bundle after writing the review. "
                         "The next round reviews your fixes. Prior round record: "
                         + (str(previous.resolve()) if previous else "none"))
            invoke(argv, True)
            recorded = invoke([verdict, "record", repo, local, "--json"])
            receipt["findings"] = recorded["findings"]["open"]
            gate = invoke([verdict, "gate", repo, local, "--max-severity",
                           "informational", "--live", "skip", "--json"])
            receipt["gate"] = gate["passed"]
            previous = output / f"round-{number}.record.json"
            previous.write_text(json.dumps(recorded) + "\n")
            if gate["passed"]:
                reason = "pass"
        except Stop as exc:
            reason = str(exc)
        receipts.append(receipt)
        (output / f"round-{number}.json").write_text(json.dumps(receipt) + "\n")
        if reason != "max-rounds":
            break
    summary = {"passed": reason == "pass", "stop": reason, "rounds": receipts}
    (output / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")
    return summary


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("repo")
    parser.add_argument("--agent", required=True, choices=("claude", "codex", "opencode"))
    parser.add_argument("--max-rounds", type=int, default=3)
    parser.add_argument("--budget", type=float, default=600, help="wall seconds")
    parser.add_argument("--output", required=True, help="new directory outside the working tree")
    args = parser.parse_args()
    if args.max_rounds < 1 or not 0 < args.budget < float("inf"):
        parser.error("rounds and finite budget must be positive")
    summary = loop(args.repo, args.agent, args.max_rounds, args.budget, args.output)
    print(json.dumps(summary))
    return 0 if summary["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
