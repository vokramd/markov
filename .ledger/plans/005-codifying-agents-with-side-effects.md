# Codifying agents that act on live services

Created 2026-10-23. Extends plan 003 (agentic codification) to an agent whose result is what it
does to a service rather than files it writes, and to splitting a broad agent prompt into
focused agent steps. The scenario is `github-workflow-example`: Claude in an OpenShell sandbox
(plan 004) triages issues and acts on `/mk-*` commands on the lab's GitHub emulator.

**Status:** phases 1–4 done (2026-10-23); phase 5 trial done (notes/codify-github-agent-trial.md): one promotion, two rejections from
agent noise; next is handling noise (open question below). Added along the way:
`markov_source_dir` (the workflow source's absolute directory: a cycle's project files must
resolve from codify's empty working directory) and the claude step's `json_schema` /
`structured_output` (focused agent steps hand native steps a parsed answer).

## Problem

`markov codify` assumes a target that (a) runs on its own, (b) writes its result as files in its
working directory, and (c) gets the same inputs every run, by replaying recorded HTTP to
candidates. The GitHub agent breaks all three: it needs a sandbox its caller creates, its result
is changes on GitHub, and GitHub's state changes with every run. Replay also refuses anything but
GET and HEAD, so a candidate cannot reset or write to GitHub at all.

Separately, the retro agent only moves mechanical jobs out of a prompt into native steps; every
job that needs judgment stays in one broad residual prompt. A prompt that does several judgment
jobs (triage, review, write code) should become several focused agent steps.

## Approach

**Make each run deterministic and its result a file.** A reset step puts the service in a
fixture state before every run, and a snapshot step writes the service's resulting state as
stable files (no ids, timestamps or SHAs). The target is a self-contained cycle:

```
github-agent-cycle:  setup-openshell → reseed-github → create sandbox → github-agent
                     → snapshot-github;  always: delete sandbox
```

Codify then compares snapshots exactly as it compares any outputs (content judge while an agent
produces them). The project pieces exist: `reseed-github` (fixture: `fixtures/markov-test-repo.yaml`),
`snapshot-github` (`scripts/snapshot-github.py`); snapshots of identical states are byte-identical.

**Fixed workflows.** The retro agent may edit the target and everything it reaches. The reset,
the snapshot and the cycle itself measure the agent and must not change. `fixed` names workflows
removed from the editable set; the retro prompt lists them as off limits, and a candidate that
changes one is rejected by the existing "only editable files may change" check.

**Live hosts.** Hosts the cycle resets itself are tunnelled through the codify proxy untouched,
for the live run and candidates alike (the proxy already does this for the claude CLI's hosts).
Tunnelling, not forwarding: a forwarded HTTPS request is re-signed with codify's CA, which steps
pinning their own CA (`tls_ca_cert`, the snapshot script's `cafile`) reject. The retro agent still
sees these calls in step outputs and events. The docs must say plainly: only for services the
workflow resets before acting, never for one it cannot reset. (The agent's own traffic in the
sandbox never reaches the codify proxy; it goes through the OpenShell gateway.)

**Split passes.** The retro prompt gains a third move. Each pass still moves exactly one job, in
this order of preference: native steps; a script; a focused agent step carved out of the broad
one (its own short prompt, a structured result native steps consume, its own model and limits,
`for_each` per item); leave it. The progress measure is agent cost and tokens, which codify
already records, not the number of agent steps (splitting raises the count while shrinking each).

## Phases

1. **Project restructure** (github-workflow-example): `setup-openshell`, `github-agent-cycle`,
   `main` calling the cycle; snapshot records PR branches by PR number. Verified with a full run.
2. **Fixed workflows and live hosts in codify:** `--fixed` and `--live-host` on `markov codify`,
   `fixed` and `live_hosts` on the `codify` step, the retro prompt's off-limits list; reference
   docs; tests (a fixed file changed rejects the candidate; a live host is tunnelled, not
   replayed or refused).
3. **Split passes:** retro prompt changes; check the claude step returns machine-readable
   results native steps can parse (a JSON object in `result`, read with `from_json`); add a
   structured-output option to the claude step if that is unreliable.
4. **IDE:** the Codify dialog gains fixed workflows and live hosts.
5. **Trial:** codify `github-agent-cycle` with the fixed and live settings; record the result.

## Open questions

- Agent noise: two runs of the current version can differ in structure (one opens a PR, one
  stops short). If that rejects good candidates, run the baseline twice and accept a candidate
  that matches either.
- Whether `fixed` should default to "everything the target reaches except the workflows that
  hold agent steps", so a cycle needs no flags.
