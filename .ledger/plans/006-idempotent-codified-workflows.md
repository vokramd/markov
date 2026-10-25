# Codified workflows that page, fan out and are safe to run again

Created 2026-10-24. Follows plan 005. The loose trial (iterations 7–14) moved most of the GitHub
agent's mechanical work into native steps, but the result only works on a freshly reseeded,
one-page repository:

- **No paging.** `fetch-issues` asks for `per_page=100` and stops there; `http_request` had no
  way to follow pages or read the `Link` header.
- **Not idempotent.** `reply-pong` posts "pong" for every `/mk-ping` on every run. Codify measures
  a single run from a clean reset, so a workflow that repeats its actions passes.
- **One flat workflow.** Item work (triage, code, review, revise, ping) sits as parallel steps
  in one workflow instead of reusable per-item sub-workflows.

**Status:** phases 1–4 done (2026-10-24; phase 3 cycle checked with v0008: it misses #1, #9,
#23, #140 and redoes 4 things on the second run). Phase 5, the trial, next.

## Approach

**Page natively.** `http_request` gains `paginate` (phase 1).

**Measure idempotency.** The cycle (a fixed workflow) runs the agent twice without a reset:
reseed → agent → snapshot → agent → snapshot the second run's changes. The second run should
change nothing. Its changes are an output the judge reads: a candidate may not change more on the
second run than the current version does. The current version fails this from the start (it
pongs again), which gives codify something to fix rather than a failed baseline.

**A fixture that exercises both.** About 150 issues, generated: untriaged issues and a `/mk-ping`
beyond the first page of 100 (lists come newest first, so that is the oldest issues: #1, #9,
#23), one issue with more than 30 comments (comment paging), and work that was
already done: a `/mk-ping` already answered by markov-bot (skip), a `/mk-ping` after an older pong
(answer), a `/mk-code` whose PR exists (skip), issues already labelled. Bot replies are posted
with the bot's token. The snapshot also writes `changes.json` (what differs from the fixture) so
the judge reads changes, not 150 issues.

**Retro prompt.** The aim is a good balance between what is deterministic and what is agentic.
Agents are not queueing systems and are bad at math: they lose track over hundreds of items in one
session, miss items, repeat items, and miscount. So the workflow does the listing, paging,
counting, filtering, sorting, comparing with what was already done, deciding what is pending,
scheduling and retrying, and any exact matching or arithmetic. Agents do judgments on one item
per call: classify it, choose among options, write code or text, review, decide whether two
things mean the same. An agent is never the loop, the counter or the bookkeeper. The rules below
follow from this.
- State before action: every write is a `for_each` over a list computed natively (wanted vs
  existing), e.g. `pending_pings` = `/mk-ping` comments with no later bot reply. Agents see only
  items that still need a decision.
- One sub-workflow per kind of item, named for its job (`triage-issue`, `code-issue`,
  `review-pr`, `revise-pr`, `answer-ping`), taking one item, checking its own state, called by
  `for_each` from a main workflow shaped gather (paged) → compute pending → fan out.
- Lists come from paginated requests checked with `assert: not x.truncated`.
- One decision per agent call, not a work queue. A prompt that hands the agent a list of work
  items to go through ("these issues; handle the ones that need it") becomes a `for_each` over a
  per-item sub-workflow: one agent call per item, given that item's data, with the list computed
  and filtered to pending items natively. Per-item calls keep the prompt the same size however
  big the repository gets, give one structured answer per item, can check their own item's state,
  and run concurrently. v0008's `ask-claude` is the case to fix: it templates all 150 issues,
  every open item's comments and the open PRs into one prompt and leaves the looping to the agent.
- Lists that are context for the one decision stay in the prompt: the labels to choose from for
  this ticket (better still, an `enum` in the answer schema), this issue's comments, this PR's
  files, candidate duplicates to compare against. The test is whether the call makes one judgment
  or loops. A call over many items also stays when the judgment itself spans them (dedupe,
  ranking, a summary), over a compact list (numbers and titles), with the reason in the step's
  `description`.
- Evidence for that: per agent call, how many items its rendered prompt carried and its input
  tokens; the judge does not count a candidate as better on this while a per-item call's prompt
  grows with the size of the repository.

## Phases

1. **`http_request` pagination** (done): `paginate: true` follows `Link` rel="next"; a map picks
   `next` (link or a body path to the next URL), `cursor` + `cursor_param`, or `page_param`
   (count up until an empty page), with `items` (body path to the list) and `max_pages`
   (default 10; `truncated: true` when it stops early). Outputs `headers` on every request, and
   `pages` and `truncated` when paginating. Tests: each paging style against a local server,
   max_pages, malformed settings; a real run against a GitHub-style server (150 issues, 2 pages,
   `state=all` kept).
2. **Emulator** (done 2026-10-24, in breadboard's github-emulator checkout: 7b889bc and the
   internal-host fix after it): next/prev/first/last links keep the request's query and use the
   public base URL (they had dropped the query, and named the in-cluster host on breadboard).
   The same round added the org membership API and owners for admin-created orgs, which
   github-workflow-example's `bootstrap-github` relies on. Checked live: `paginate: true` on
   `per_page=1&since=...` followed one link and collected both comments of issue #1.
3. **Fixture and cycle** (github-workflow-example; fixture side done 2026-10-24, the two-run cycle
   untested until OpenShell is installed here):
   - `bootstrap-github` (new) makes sure the org (with the admin as owner), `markov-bot` and a
     working bot token exist; the token is minted only when the saved one (`GITHUB_TOKEN_FILE`,
     default `.state/github-token`, 0600) stops working, marked `secret`, and passed by the cycle
     to every workflow that uses it as `GITHUB_TOKEN`.
   - `scripts/generate-fixture.py` writes `fixtures/markov-test-repo.yaml`: 150 issues (30 closed),
     3 PRs (#151 the bot's PR for #45's /mk-code, #152 reviewed by the bot after /mk-review, #153
     with an unanswered /mk-review), 86 comments (35 on #140, its /mk-ping past the first page),
     and an `expected` list: triage #1, #9, #57, #104, #133, #150; pong on #1, #23 (second
     /mk-ping only), #112, #140; PRs for #1 and #126; a review of #153; nothing on a second run.
     Left alone: #12 (answered), #45 (has its PR), #77 and #88 (closed), #121 (closed, untriaged).
   - `reseed-github` creates branches, files and PRs, posts comments and reviews with the
     author's token (`author: bot` as markov-bot), closes closed issues, and checks issue and PR
     numbers. About 55 s. Checked: a snapshot right after it matches the fixture exactly.
   - The cycle snapshots the fixture state (`.snapshots/fixture`), runs the agent, snapshots
     (`snapshot/`), runs it again in the same sandbox and snapshots (`.snapshots/second-run`).
     `scripts/diff-snapshots.py` writes `snapshot/changes.json` and
     `snapshot/second-run-changes.json` (per-issue and per-PR changes, multiset diffs of lists,
     `counts.total`).
4. **Codify** (done 2026-10-24):
   - `--repeat-changes path[:key]` (default key `counts.total`; IDE codify form and control
     `repeat_changes`): the current version must write it; a candidate that changes more on the
     second run than the current version is rejected before the judge; the counts go in
     `validation.json` (`repeat`), the history, and the loose judge's prompt.
   - Evidence `agent_calls.md`: every agent call's rendered prompt size (the claude step's new
     `prompt_chars` output) and input tokens, per step (min/median/max) and per call.
   - Retro prompt, both modes: the deterministic/agentic split (agents are not queues and are bad
     at math; lists as context for one decision are fine, work queues are not). Loose mode: gather
     (paged, asserted complete) → compute pending → fan out to per-item sub-workflows named for
     their job; state before every action; aim for no repeat-run changes.
   - Loose judge: the current version may have missed items or redone work; acting on missed
     items the job calls for, or not redoing finished work, is doing the job better.
   - Tests for the check, the evidence, the prompts and the history; docs in `cli.md`
     ("Running it twice", "What is deterministic and what is agentic").
5. **Trial** from v0000 in loose mode, driven through the IDE.

## Open questions

- Running the agent twice doubles a measurement's cost; once the item work is native the second
  run should be nearly free.
- Whether the idempotency check belongs in codify itself (a `rerun` option) rather than in each
  project's cycle.
