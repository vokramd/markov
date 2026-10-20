# Bug: set_fact facts see siblings in random order

## Status

Fixed 2026-10-20. `evalFacts` evaluates every fact against the context as it was before the step and no longer writes into the context while iterating; the step stores all facts afterwards. Facts in one step cannot refer to each other; documented in `step-types.md` (use two `set_fact` steps). Test: `TestSetFactFactsSeeContextBeforeTheStep` (50 repetitions). No workflow in the workspace used sibling references.

## Summary

`set_fact` iterated its `vars` (a Go map, unordered) and wrote each fact into the context as it went, so a fact referring to a sibling saw the new value or the old one depending on map iteration order.

## Reproduction

Found by the plan 003 codification loop (run 2, iteration 4): the retro agent wrote

```yaml
- name: pick_url
  type: set_fact
  vars:
    story_url: "{% if story.story_items.body.url %}...{% endif %}"
    story_line: "- [{{ story.story_items.body.title }}]({{ story_url }})"
```

Across ten `for_each` iterations some lines had the URL and some had `()`. The fixture validator rejected the candidate.

## Impact

Silent, nondeterministic data corruption in any `set_fact` whose values reference each other; a workflow could pass a test and fail the next run.
