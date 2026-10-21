# Bug: Registered sub-workflow and for_each results carry the caller's whole context

## Status

Open. Found 2026-10-20 in the plan 003 Haiku loop; worked around in the codification driver.

## Summary

A sub-workflow starts from a copy of the caller's context, and `register` on the call (or on a `for_each`) stores the child's entire final context, so each registered result contains everything the caller had. In a recursive loop the registered result is inherited by the next iteration and registered again, nesting the previous one: the codification loop's `pass` grew 52 KB → 108 KB → 145 KB → 231 KB → 270 KB over five iterations. A task step that serialized a 10-item `for_each` result (`{{ fetched | to_json }}`) inside that loop rendered ~1.5 MB and failed its agent's `max_tokens` limit; the same task run alone (as the validator runs it) was small and passed.

## Evidence

`examples/codification/hn-top10-haiku.codify/state.db`: `runs.vars_json` of each iteration's `...-task` sub-run; iteration 4's `do_task` failed with `claude: limit exceeded: max_tokens`.

## Workaround

Don't register large sub-workflows in loops; pass results through files or small `set_fact`s (the driver now reads the codify pass outcome from `validation/result.json`).

## Possible fixes

- Register only what the child set itself (its `vars` plus registered outputs and facts), not the inherited context.
- An explicit `outputs:` on workflows naming what a caller receives.
- For direct `for_each`, register each item's output (and the item) rather than the iteration context.

Each changes what existing workflows see under a register name, so it needs a compatibility decision (ADR).
