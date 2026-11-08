# Two audiences: repeated agent jobs, and evals

Created 2026-11-07, after the competitive research
([notes/competitive-positioning.md](../notes/competitive-positioning.md)). "Workflows that chain
agents with deterministic steps" is taken (Archon, Claude Code workflows, gh-aw); what Markov does
that others don't serves two groups. This plan is what would make Markov compelling to each.

**Status:** proposed; nothing started.

## Who

1. **Engineers running the same agent job over many items, again and again, on their own
   infrastructure**: nightly issue or alert triage, a CVE or dependency fix across 50 repos,
   failed-job investigation, labelling, release notes. Platform, SRE and ops engineers who know
   Ansible fit best: the YAML reads like what they write, and they have been burned by playbooks
   failing halfway. They need hard limits on every agent call (both vendors), per-item resume,
   native HTTP/k8s/ansible steps so the mechanical part costs nothing, codify (a nightly job's cost is
   worth shrinking), and their own cluster and secrets.
2. **People evaluating coding agents**: skills, prompts and CLAUDE.md, agent harnesses, and models,
   which is how Markov is used today (Breadboard's strat-workflow benchmark shaped
   `ignore_errors`, `for_each_when`, native expressions, `csv`/`seq`; markov-harness). They need the
   real CLI with the user's real config, case × variant × model × repetition matrices, resume after
   crashes and rate limits, deterministic graders and judges, transcripts, and results they can
   compare.

Both start local-first, on a laptop with an API key. markovd follows when a job or an eval suite
proves itself.

Send elsewhere: one-off jobs (Claude Code workflows), GitHub-event bots (gh-aw), issue-to-PR with
chat (Archon), SaaS glue (n8n), Python agent products (LangGraph, Temporal), short scripts.

## The eval landscape (research 2026-11-07)

Notes: `research_notes/Markov competitive landscape/eval_tools.md` (outside the repo).

- **`claude plugin eval`** (Anthropic, Claude Code 2.1.269+), the closest for skills and plugins:
  the real Claude Code in a fresh workspace and home per case, 3 runs per case, a with/without-plugin
  baseline, free graders (regex, tool_used, tool_order, file_exists) plus `llm` and `baseline` judges,
  MCP mocks recorded and replayed, a cost ceiling, JSON and HTML reports, CI exit codes. No run-to-run
  comparison, one model per run, no pass@k, no project CLAUDE.md, no Codex, plugins only.
- **Inspect + inspect_swe** (MIT): `claude_code()` and `codex_cli()` agents in per-sample sandboxes,
  model calls proxied through Inspect, `eval_set` over tasks × models with sample-level resume,
  epochs and reducers, `inspect view`. The most rigorous for comparing harnesses and models.
- **Harbor** (Apache-2.0, Terminal-Bench 2.0): a container per trial, local or cloud, a dataset
  registry.
- **@vercel/agent-eval**: Claude Code, Codex, OpenCode, Gemini and Cursor in sandboxes against repo
  tasks, hidden Vitest checks, runs and caching, a viewer. The closest twin in spirit.
- **Promptfoo** (MIT; OpenAI announced its acquisition 2026-03-09): YAML config, a Claude Agent SDK
  provider (not the CLI), `--repeat`, trajectory assertions, a viewer, red teaming.
- **Braintrust, LangSmith, Langfuse, Phoenix, Weave, DeepEval**: datasets, experiments, dashboards;
  none runs real CLI agents in sandboxes. Feed them, don't fight them.
- **Hermetic agent evals by recording HTTP**: not found for unmodified CLI agents. Nearest:
  plugin eval's MCP mocks, Inspect's model cache (unverified for proxied agents), SDK-level VCR
  libraries. Honest limit: replaying the model only holds while the agent's requests stay
  identical, so the use is re-grading recorded trials for free, freezing external APIs under a live
  model, and deterministic CI smoke tests; not testing a changed skill against a frozen model.

Where Markov wins for evals: the thing under test can be a whole workflow (a harness, or skill +
CLAUDE.md + hooks together); the user's real CLI and config; enforced limits per trial; per-item
resume; record and replay of all HTTP; the IDE to step through one failing trial.

## Approach

### A. Proof first (both audiences)

1. **A measured codify story on a real nightly job**: cost per run, failures, and what moved per
   version over a couple of weeks. The launch lead.
2. **A measured eval story**: e.g. two versions of a Claude Code skill (or markov-harness against
   Claude Code) on 20 tasks, with test-based graders and a judge, repeated, with pass rates, spread
   and cost. Written up as "what I found", not as a product pitch.
2a. **Codify, then re-evaluate**: run the eval suite from (2) against a workflow before and after
   codify. Cost down, pass rate unchanged (or not, and say so) is the one proof no competitor can
   produce, since none has evals and codify on one engine. It also covers codify's blind spot: the
   eval exercises branches the recorded run never took. See "Breadth, shown as a lifecycle" in
   [notes/competitive-positioning.md](../notes/competitive-positioning.md).

### B. Trust for unattended runs

3. **Run budget**: `max_budget_usd` across every agent call in a run, enforced live; a per-run and
   per-item cost breakdown in `markov status` and markovd; Codex cost estimated from tokens.
4. **Fallback to the agent for codified steps**: a native step that meets something the recording
   never had falls back to the agent prompt it replaced, finishes, and flags itself for re-codifying.
   Deterministic by default, an agent when the world surprises you. Nobody does this for workflows.
5. **Approvals where people are**: a gate resumed from a Slack message or a GitHub comment (when a
   user asks).

### C. Evals as a first-class use

6. **Results summary**: JSON plus a self-contained HTML report, pass rate per case, variant and
   model, with cost, duration and tokens.
7. **Comparison**: between runs and between with/without arms, regression flags, CI exit codes on
   thresholds.
8. **Repetition statistics**: unbiased pass@k, pass^k, stderr or confidence intervals, high-variance
   cases flagged.
9. **A fresh workspace per trial**: copy or git clone, optionally in OpenShell, kept on failure.
10. **An eval-suite layout with graders**: tool_used and tool_order read from transcripts,
    file_exists, regex, run-the-tests, a majority-vote judge; a built-in with/without arm for skills,
    CLAUDE.md and plugins.
11. **Record and replay as a feature**: record on first run, strict replay that fails on unrecorded
    requests, tolerant matching, recordings committed per case. Shared with codify.
12. **Exporters**: Langfuse, Braintrust, LangSmith, Inspect's log format; adapters to run Harbor or
    Inspect datasets.

### D. Authoring without a blank page

13. **Agents write the workflow, validate checks it**: a strict mode where findings stop a run, a
    JSON Schema for the format, validate over MCP or in the Claude Code plugin, and reads of
    `structured_output` fields checked against `json_schema`.
14. **Turn a session into a workflow**: from a Claude Code transcript of a job done by hand, a
    Markov workflow ready for codify.
15. **A starter pack**: three or four ops jobs (triage, cross-repo fix, failed-job investigation,
    release notes) and one eval suite, each runnable in five minutes with an API key.

## Phases

1. **Proof and trust**: the two measured stories (1, 2), codify then re-evaluate (2a), the run
   budget and cost report (3).
2. **Evals core**: results summary, comparison, statistics, fresh workspaces (6–9).
3. **The feature nobody has**: fallback to the agent (4), and record/replay as a feature (11).
4. **Authoring**: strict validate, JSON Schema, MCP or plugin, session to workflow (13, 14), with
   the starter pack (15) as their showcase.
5. **Graders, exporters, approvals** (10, 12, 5) as users ask.

## Open questions

- Is Inspect + inspect_swe a competitor or a target? Running Markov workflows as Inspect agents, or
  exporting to Inspect's log format, might reach eval users faster than our own viewer.
- How much of the results view belongs in markov (JSON and HTML report) and how much in the IDE or
  markovd (charts, run history)?
- Whether the eval story or the codify story leads the launch; the eval one is what the maintainer
  does every day. Either way, the README and site frame both as stages of one lifecycle (build,
  evaluate, run, shrink, re-evaluate), and 15's starter pack doubles as the gallery that shows the
  range.
