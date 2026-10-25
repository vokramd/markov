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

**Status:** phase 1 done (2026-10-24).

## Approach

**Page natively.** `http_request` gains `paginate` (phase 1).

**Measure idempotency.** The cycle (a fixed workflow) runs the agent twice without a reset:
reseed → agent → snapshot → agent → snapshot the second run's changes. The second run should
change nothing. Its changes are an output the judge reads: a candidate may not change more on the
second run than the current version does. The current version fails this from the start (it
pongs again), which gives codify something to fix rather than a failed baseline.

**A fixture that exercises both.** About 150 issues, generated: untriaged issues past #100, a
`/mk-ping` past #100, one issue with more than 30 comments (comment paging), and work that was
already done: a `/mk-ping` already answered by markov-bot (skip), a `/mk-ping` after an older pong
(answer), a `/mk-code` whose PR exists (skip), issues already labelled. Bot replies are posted
with the bot's token. The snapshot also writes `changes.json` (what differs from the fixture) so
the judge reads changes, not 150 issues.

**Retro prompt.**
- State before action: every write is a `for_each` over a list computed natively (wanted vs
  existing), e.g. `pending_pings` = `/mk-ping` comments with no later bot reply. Agents see only
  items that still need a decision.
- One sub-workflow per kind of item, named for its job (`triage-issue`, `code-issue`,
  `review-pr`, `revise-pr`, `answer-ping`), taking one item, checking its own state, called by
  `for_each` from a main workflow shaped gather (paged) → compute pending → fan out.
- Lists come from paginated requests checked with `assert: not x.truncated`.

## Phases

1. **`http_request` pagination** (done): `paginate: true` follows `Link` rel="next"; a map picks
   `next` (link or a body path to the next URL), `cursor` + `cursor_param`, or `page_param`
   (count up until an empty page), with `items` (body path to the list) and `max_pages`
   (default 10; `truncated: true` when it stops early). Outputs `headers` on every request, and
   `pages` and `truncated` when paginating. Tests: each paging style against a local server,
   max_pages, malformed settings; a real run against a GitHub-style server (150 issues, 2 pages,
   `state=all` kept).
2. **Emulator:** next/prev/last links keep the request's query (`state=all`, filters); today
   `issues.py`, `comments.py`, `pulls.py` and others drop it, so page 2 of `state=all` is page 2
   of open issues. Deployed on testbox.
3. **Fixture and cycle** (github-workflow-example): generated 150-issue fixture with done work;
   reseed posts bot replies as markov-bot; the cycle runs the agent twice; snapshot writes
   `changes.json` and the second run's changes.
4. **Codify:** loose judge rule for second-run changes; retro prompt rules (state before action,
   per-item sub-workflows, paged lists).
5. **Trial** from v0000 in loose mode, driven through the IDE.

## Open questions

- Running the agent twice doubles a measurement's cost; once the item work is native the second
  run should be nearly free.
- Whether the idempotency check belongs in codify itself (a `rerun` option) rather than in each
  project's cycle.
