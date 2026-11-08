# Competitive positioning (2026-11-07)

What we concluded from the competitive research before looking for first users. The full report,
with sources and a "why not X?" answer for about 100 alternatives, is
[competitive-landscape-research.md](competitive-landscape-research.md); its notes are outside the
repo in `vokramd/research_notes/Markov competitive landscape/`. Markov's own capabilities in it came
from a brief, not from re-reading the code; one correction: only the `claude` step has a USD limit
(`codex` has max_tool_calls, max_tokens and max_duration, and Codex reports no cost).

## The headline is taken

"YAML workflows that chain Claude Code and Codex with deterministic steps" is no longer ours:

- **Archon** (MIT, ~24k stars, weekly releases, 75 contributors): a YAML DAG of agent, bash, loop,
  wait and child-workflow nodes driving Claude Code (Agent SDK) and Codex (app-server). Its rule,
  "YAML coordinates, code computes, agents judge", is our thesis. The hardest "why not X?".
- **Claude Code dynamic workflows and scheduled tasks**: first-party, and Claude writes the script.
- **GitHub Agentic Workflows** (v1.0.1): Markdown compiled to Actions, Copilot/Claude/Codex/Pi,
  budgets on by default, the best security model in the category.
- Google ADK 2.0, Microsoft Agent Framework, LangGraph, Kestra: "deterministic workflow with agent
  nodes" is their main feature now.

Stop leading with: mixing agent and deterministic steps, JSON-schema answers (both CLIs have them),
durable resume, scheduling, vendor neutrality. Live transcripts and human gates are only partly ours.

## What is still ours

- **`markov codify`.** The idea has prior art (Skyvern and Stagehand caching, TraceCompiler,
  Compiled AI, plan caching, UiPath's "deterministic on top"), but nothing found combines: one job
  moved per pass, recorded HTTP replay as the gate for promotion, strict and loose equivalence,
  splitting a broad agent step into per-item calls, beyond the browser. Say "we found no
  equivalent", not "unique": UiPath and n8n are each a step away.
- **Limits that kill the step on breach, for both CLIs.** Codex has no caps of its own; Archon's USD
  cap is Claude-only and its run budget refuses the next attempt rather than stopping one; Conductor
  audits by default; Claude Code workflows only warn.
- **Ops steps:** http_request (pagination, SSE), k8s_job, ansible, OpenShell, and markovd on Kubernetes.
- **Fan-out that is cheap and resumable per item.** Fan-out itself is common (Claude Code workflows
  run up to 1,000 agents, Actions a 256-job matrix, Conductor and Windmill have parallel loops).
  What differs: `for_each` works on any step, native steps do each item's mechanical work for zero
  tokens, `for_each_when` filters deterministically, stable keys (`for_each_key`) let a resume skip
  finished items, concurrency is bounded, and each item's agent call has its own kill limits.
  Against that: a failed Claude Code fan-out reruns agents that already finished; Archon fans out
  only over child workflows (400 items, 400 child runs); Conductor's resume reruns sub-workflows from
  scratch and resets the budget.
- **Recursion** (2026-11-07): a workflow may call itself, with `when` as the base case and every
  call a resumable sub-run; `max_call_depth` (64 by default) fails a runaway recursion with its call
  chain. Patterns: tree walks (directories, dependency graphs, issues with sub-issues) and "split
  until small enough", where an agent makes one judgment (split or handle?) and each leaf gets its
  own focused call, a judgment codify could later replace with a native rule. Prior art: Argo
  Workflows has recursive templates with a `when` guard; code frameworks recurse natively
  (LangGraph caps it with `recursion_limit`). Not yet checked for Archon, Conductor, gh-aw or Claude
  Code workflows.
- **A format an agent can check before it runs.** Templating itself is table stakes (Actions
  `${{ }}`, Argo, Kestra's Pebble, Conductor and Ansible Jinja2, n8n JavaScript). What may be ours is
  `markov validate`: strict YAML, every template and expression parsed (unknown filters included),
  variables a workflow cannot see (plan 008 scoping), unknown workflows and `workflow_names`, and
  `set_fact` values that would be stored as booleans, all before a run. Agents write and rewrite
  these workflows (codify's retro agent, the planned Claude Code plugin), so checking them up front
  matters twice. Also: templated workflow names (dispatch by name, what markov-harness needs) and
  exact `{{ expr }}` values kept as data, with `pluck`/`flatten` to collect nested fan-out results.
  Compared with the competitors' validators (research 2026-11-07, notes in
  `research_notes/Markov competitive landscape/templating_and_validation.md`):
  - **Stronger**: parsing every template and expression (Argo's lint skips `{{= }}` expressions;
    Kestra, Windmill and n8n parse none before a run; Archon only `when:`); scope-aware variable
    checks (only Conductor and actionlint do comparable reference checks); a templated sub-workflow
    name checked against `workflow_names` (Archon, Conductor, Actions and Argo need literal names;
    Ansible, n8n and probably Kestra compute names and check nothing). Native types from an exact
    `{{ }}` like Ansible 2.19 and the JavaScript tools; Argo, Actions, Kestra and Archon produce strings.
  - **Weaker**: no type checking (actionlint type-checks expressions; Conductor and Archon check
    output fields against declared schemas); findings don't block `markov run` (Conductor exits 2,
    gh-aw's compile is a gate); `markov schema` is a custom format, not a JSON Schema editors use;
    no MCP or language-server surface (gh-aw ships `gh aw mcp-server` and an authoring skill); no
    expression security checks (gh-aw allowlists expressions); no stubbed dry run (Archon's
    `--dry-run --stubs`).
  - Claude Code dynamic workflows are plain JavaScript with no validate command.
  - Comparable to Conductor overall, behind actionlint and gh-aw on types, security and agent
    tooling. Say "checks templates, scopes and dispatch before a run", not "the best validation".
- **Guards for unattended agents**: `max_output` (a runaway agent can't produce gigabytes into the
  run), secret masking in events and logs, `rescue`/`always`. Individually table stakes; together an
  answer to the "unattended agents scare me" objections.
- **Rule-engine gates, and the IDE's breakpoints and step-through.**
- **The format can express the agent loop itself** (markov-harness, below).

## Breadth, shown as a lifecycle (2026-11-08)

Each competitor covers one slice: running agent jobs (Archon, gh-aw, Claude Code scheduled tasks),
evaluating agents (Inspect, `claude plugin eval`, Harbor), orchestrating infrastructure (Argo,
Ansible, Kestra), building agent loops (LangGraph, the Vercel AI SDK), and shrinking agent work into
code (nobody found). Markov does all five in one format: ops pipelines with k8s_job and ansible
steps, eval matrices, a whole agent harness (markov-harness), recursion, and codify.

Breadth is hard to pitch: "it does everything" reads as "it's for nobody in particular", and generic
orchestrators scored about 30 on HN. People adopt a tool for one sharp reason and stay for the
breadth. So present it as one lifecycle of the same workflow file, not a feature list:

1. **Build**: chain agents with native steps; debug in the IDE.
2. **Evaluate**: run the workflow, or the skill, prompt or harness inside it, over cases × variants ×
   models × repetitions.
3. **Run**: on a schedule, with kill limits, per-item resume, markovd on your cluster.
4. **Shrink**: codify moves the no-judgment work into native steps, gated by replay.
5. **Re-evaluate**: the same eval workflow proves the shrunk version scores the same.

Steps 4 and 5 together are what no one else can offer, because no one else has evals and codify on
one engine. "Codify cut the cost by 80% and the eval shows the pass rate didn't move" is stronger
than either half; it also answers codify's weakest point (its oracle covers only the recorded run).

How to use it: each launch post leads with one sharp story (the eval write-up or the codify one); the
README and site frame Markov as one engine for the whole life of an agent workflow; an examples
gallery shows the range (an ops job, an eval suite, markov-harness, a recursive walk, a codify run).
The breadth only persuades if it is visible, and today most of it lives in the maintainer's own use
and in this ledger, not in public examples.

What codify needs is what we built first: native steps for work to land in, an engine that runs
the same workflow live and against a recording, and per-step limits and token counts to measure the
saving. "Before codify" was the foundation, not wasted.

## Concede openly

Scripts for flows of up to about three agent calls; gh-aw for PR bots and issue triage; Claude Code
workflows for one-off jobs; n8n for SaaS glue; Temporal or LangGraph for Python products; Archon for
an issue-to-PR factory with chat integrations. Saying so in the README earns more trust than a
feature grid.

## Ideas to pursue

1. **One measured codify story** leads the launch: a workflow that starts all-agent, the passes it
   went through, and tokens, cost and wall time per run before and after. On HN, launches with one
   concrete result scored 126–337; generic orchestrators about 30.
2. **Same task, two ways**: a Claude Code dynamic workflow, where every fetch, filter and write is an
   agent call (its script has no shell, file or HTTP access), against a Markov workflow where native
   steps do the mechanical work and agents make only the per-item judgments. Compare tokens, cost,
   time, and whether repeated runs agree. Codify is how you get from the first to the second.
3. **markov-harness as proof of expressiveness**: "we built Claude Code in Markov's YAML, and you can
   step through it in a debugger". It needs things the others lack as workflow primitives: a
   streaming model call (`http_request` with SSE), dispatch by name (`tools.{{ block.name }}`), a loop
   on the reply, fan-out with mixed concurrency, interactive input, and local tools. Claude Code
   workflows and gh-aw can't; Archon and Conductor would put the loop in a bash or script step;
   Windmill and Kestra could run one on a server worker but not as a terminal chat; code frameworks
   can, but then it is a program, not a workflow. (From docs, not tried.) The loop being a workflow
   means breakpoints on a tool call, limits and progress events on every turn, and in principle codify
   on the harness itself.
4. **Separate two meanings of "codify"**: Anthropic says workflows "codify the orchestration"; ours
   codifies the work inside agent steps.
5. **Honest comparisons in the README / docs**: the "why not X?" answers for scripts, gh-aw, Claude
   Code workflows, Archon, n8n, Temporal/LangGraph.
6. **A "split until small enough" example**: an agent decides whether a task (a large diff, a
   directory, an epic) needs splitting; the workflow recurses on the parts and gives each leaf one
   focused agent call. Shows recursion, fan-out and "one judgment per call" together.
7. **Close the validation gaps** (from the research): publish a JSON Schema for the workflow
   format; a strict mode where validate findings stop `markov run`; validate over MCP or in the
   Claude Code plugin; check reads of a step's `structured_output` fields against its `json_schema`.
8. **Lead local-first**: `markov run` on a laptop first, markovd and Kubernetes second (an HN
   commenter called a Kubernetes requirement a "non-starter").

## Evals

Markov is already used for evals (Breadboard's strat-workflow benchmark, markov-harness), and that
is a second audience: see [plan 011](../plans/011-ops-jobs-and-evals.md) for the eval landscape
(`claude plugin eval`, Inspect + inspect_swe, Harbor, @vercel/agent-eval, Promptfoo, the SaaS
platforms). Hermetic agent evals by recording HTTP were not found for unmodified CLI agents.

## Rough edges first users will hit

- **No list literals** in expressions: `[path]` does not parse; use a YAML list (`["{{ path }}"]`).
- **A plain `set_fact` string is a boolean expression**: `leaves: "kids | pluck:'leaves'"` stores
  true or false. `markov validate` now warns when such a string ends in a data filter
  (2026-11-07; no false positives over the examples, 12 directory workflows and 124 YAML files with
  set_fact in the workspace).
- **YAML turning into a bad language**: the usual critique of templated YAML. The answer is
  Ansible's and Actions': keep the YAML declarative, put logic in script steps, and check it all
  before running.

## Risks to answer before launch

- **Subscription logins in automation.** Anthropic allows the unmodified `claude` CLI with a user's
  own subscription, but plan limits assume "ordinary, individual usage", products should use API
  keys, and third parties may not "collect, store, or intermediate" credentials. Heavy scheduled or
  fan-out runs are a grey zone, and a shared or hosted markovd login would be intermediating. OpenAI
  recommends API keys for Codex automation but documents ChatGPT login on trusted runners. Write an
  "Is this allowed?" FAQ steering scheduled and markovd runs to API keys. Not legal advice.
- **One maintainer, v0.1.0.** Expect "day seven: never open this repo again". Visible cadence,
  Discussions answered, a roadmap.
- **Codify's oracle covers only what the recorded run did**: branches it never took are untested, a
  candidate that fetches something new is rejected, the judge can be lenient, and a codified
  workflow has no fallback to the agent when the world changes. Document these, and consider a
  fallback.
