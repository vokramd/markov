# Codification phase 1: first live iteration

2026-10-20, `examples/codification/hn-top10`, `./codify.sh 1`, codify dir `examples/codification/hn-top10.codify/` (gitignored).

| | agent steps | native steps | tokens | cost (USD) | time |
|---|---|---|---|---|---|
| v0 task run (live API) | 1 | 1 | 13,667 | 0.155 | 16.7 s agent |
| retro pass (one-time) | 1 | | 43,685 | 0.531 | 55 s |
| v1 candidate (fixture) | 0 | 2 | 0 | 0 | 0.8 s |

- The retro agent read the evidence, judged the whole task mechanical, replaced the `claude` step with `script_exec` of a 36-line `scripts/task/hn_top10.py`, ran `markov validate`, and wrote `retro.json` (`changed: true`, `remaining_agent_steps: 0`).
- The validator ran v1 against the recorded fixture; its output matched the contract line for line; promotion archived v0 as `versions/v0000` and installed v1 as `versions/v0001`.
- v0 produced a correctly formatted file on the live API.
- Break-even: retro cost ≈ 3.4 v0 runs.
- The example was rolled back to v0 afterwards so the checked-in baseline stays the single-prompt version.

## Observations

- The retro collapsed everything into one Python script in a single pass, despite the "one coherent change" instruction (the prompt allows full codification when the task is plainly mechanical). That is valid codification but skips the gradual native-step decomposition the demo is meant to show. Options: require native steps where one exists and scripts only for what the DSL cannot express; forbid removing the last `claude` step in one pass; or add a judgment step (topic tagging) so a residual agent must remain.
- A native-steps-only codification is harder than it should be because of the three open bugs (float IDs, direct `for_each` register losing output, completion-order register lists). An agent that hits them may fall back to scripts for that reason alone.

# Run 2: gradual codification after the engine fixes and prompt tightening

2026-10-20, `CODIFY_DIR=../hn-top10-run2.codify ./codify.sh 4` then `./codify.sh 3` (continued). Fixed first: JSON ints, direct `for_each` register, `for_each` order; found and fixed during the run: `set_fact` sibling order. The retro prompt now requires one extracted job per pass, native steps over scripts (scripts need a `script_justification`, enforced by the validator), and removing the last `claude` step only as its own pass; it also reads the previous pass's retro and validation result.

| Iter | Task run (agent / native steps) | Task tokens | Task cost | Retro cost | Job extracted | Validation | Promoted |
|---|---|---|---|---|---|---|---|
| 1 | 1 / 1 | 12,046 | $0.154 | $0.413 | fetch topstories, take first 10 IDs | passed | v0001 |
| 2 | 1 / 4 | 12,217 | $0.144 | $0.378 | fetch each item (`for_each` http_request) | passed | v0002 |
| 3 | 1 / 16 | 11,372 | $0.113 | $0.383 | choose url or HN fallback (sub-workflow) | passed | v0003 |
| 4 | 1 / 27 | 11,313 | $0.112 | $0.297 | format each line | **failed**: line 2 had `()` (set_fact sibling bug) | – |
| 5 | 1 / 27 | 11,308 | $0.115 | $0.418 | format each line (avoided the failed pattern, citing the previous rejection) | passed | v0004 |
| 6 | 1 / 37 | 11,278 | $0.111 | $0.411 | write the file (`write_file`), last `claude` step removed | passed | v0005 |
| 7 | 0 / 38 | 0 | $0 | $0.179 | none | no_change, loop stopped | – |

Final v0005: `http_request` → `set_fact` slice → `assert` → `for_each` `http_request` → `assert` → `for_each` sub-workflow `task_story_link` (two `set_fact` steps) → `assert` → `write_file`. No scripts. Archived in `examples/codification/hn-top10-run2.codify/versions/`; the example was rolled back to v0.

## Observations

- Gradual decomposition works: five promoted versions, each moving one named job, all native.
- **Savings are back-loaded.** While any `claude` step remains, per-run cost only falls from $0.154 to $0.111 and tokens barely move (~12k → ~11.3k): the fixed overhead of a Claude session (system prompt, tools) dominates, and the residual agent's work shrinks to almost nothing before it disappears. Agent seconds did fall (20 s → 7 s). The cost curve is a cliff at the final removal, not a slope; deterministic coverage by step count rises steadily but is a poor proxy for cost.
- Total codification spend $2.48 against $0.154 per run saved: break-even after ~16 runs (the one-shot script codification of run 1 broke even after ~4). Gradual is safer and reviewable, but costs more; the plan should treat pass granularity as a tunable.
- The validator caught a real engine bug the agent had no way to see, and the feedback of the previous validation result let the next pass fix it.
- The driver's `report` step originally broke on quotes in validation reasons (`echo` of rendered text); it now passes JSON on stdin.

# Phase 4: the loop in one process

2026-10-20, `./codify.sh 3` starting from run 2's v0004, codify dir `hn-top10-p4b.codify`. One `markov run --workflow codify_loop --allow-reload`; one event log; one reload.

| Iter | Task (agent / native) | Task tokens | Task cost | Retro cost | Validation | Action |
|---|---|---|---|---|---|---|
| 1 | 1 / 37 | 11,254 | $0.111 | $0.279 | passed | promoted v0001, reloaded in-process |
| 2 | 0 / 38 | 0 | $0 | $0.171 | no_change | loop stopped |

A first attempt (`hn-top10-p4test.codify`) failed every candidate on an engine check (see tasks/done/codify-in-process-loop.md), costing ~$1.6.

# Haiku: the whole loop on claude-haiku-5-5

2026-10-20, `./codify.sh 8` from v0 with `task_model` and `retro_model` = `claude-haiku-5-5`.

First attempt (`hn-top10-haiku.codify`): five promotions, but iteration 4's task failed `max_tokens`: the residual prompt serialized a `for_each` result, and in the loop every element carried the driver's growing `pass` register (52 KB → 270 KB over five iterations; bugs/open/register-copies-whole-context.md). The validator, which runs the task alone, could not see it. The driver no longer registers the codify pass; it reads the outcome from `validation/result.json`.

Second attempt (`hn-top10-haiku2.codify`):

| Iter | Task (agent / native) | Task tokens | Task cost | Retro cost | Job extracted | Result |
|---|---|---|---|---|---|---|
| 1 | 1 / 1 | 16,722 | $0.0055 | $0.057 | fetch topstories, take 10 IDs | promoted v0001 |
| 2 | 1 / 3 | 14,992 | $0.0050 | $0.026 | fetch each item | promoted v0002 |
| 3 | 1 / 14 | 11,732 | $0.0035 | $0.075 | pick url or HN fallback (`task_link`) | promoted v0003 |
| 4 | 1 / 24 | 11,008 | $0.0030 | $0.019 | format each line | promoted v0004 |
| 5 | 1 / 25 | 11,493 | $0.0035 | $0.017 | write the file | promoted v0005 |
| 6 | 0 / 26 | 0 | $0 | $0.008 | none | no_change, stopped |

- Every candidate passed on the first try; no scripts. Task context stayed 2.2–2.9 KB per iteration.
- Retro total $0.20 (Opus run 2: $2.48). The agent task is now so cheap ($0.0055/run) that codifying it pays back only after ~37 runs; on Opus the same work paid back after ~16. The cheaper the agent, the weaker the economic case, and the stronger the case for codifying only expensive or frequent work.
