# Bug: for_each register collects results in completion order

## Status

Fixed 2026-10-20. Results go into one slot per input index and are compacted after the loop, so the registered list follows input order at any concurrency; skipped or never-started items are omitted. Tests: `TestDirectForEachRegisterCollectsOutputsInInputOrder`, `TestSubWorkflowForEachRegisterKeepsInputOrder` (both fail on the old code).

## Summary

With `concurrency` above 1 (default 5), the list stored by `register` on a `for_each` step is in the order iterations finished, which varies between runs, not in input order. Seen with both direct and sub-workflow fan-out over four HTTP fetches: three runs gave three different orders.

## Expected

Registered results in input order regardless of concurrency (or documented as unordered with the item index available for sorting).

## Workaround

`concurrency: 1`, losing parallelism.

## Impact

Any ordered output built from a fan-out (reports, rankings). A codified workflow can pass validation once and fail the next run by chance.
