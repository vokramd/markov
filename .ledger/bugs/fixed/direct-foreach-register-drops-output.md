# Bug: Direct for_each with register drops each iteration's output

## Status

Fixed 2026-10-20. The per-item copy keeps `register`, so the step registers its output in its own iteration context (`items[i].<register>`), which the loop collects; resumed iterations restore it the same way. Documented in `for-each.md`. Test: `TestDirectForEachRegisterCollectsOutputsInInputOrder`. The per-element copy of the whole parent context remains.

## Summary

On a typed step with `for_each` (no `workflow`), `register` collects one context per iteration, but those contexts contain only the inherited variables and the loop variable, not the step's output. The register field is cleared on the per-item copy (`docs/reference/for-each.md`), so the output is never stored.

## Reproduction

```yaml
- name: items
  type: http_request
  for_each: "top.body | slice:':2'"
  as: id
  register: items
  params:
    url: "{{ base }}/item/{{ id | integer }}.json"
```

Each element of `items` has keys `id`, `top`, `markov_run_id`, ... but no `body` or `status_code`.

## Expected

Each element carries the step's output (e.g. under the register name or as the step output fields), as the docs' `health_results` example implies.

## Workaround

`for_each` over a sub-workflow whose step registers the output.

## Impact

The direct form looks like the natural way to fetch a list of resources and silently yields nothing usable. Also each element copies the whole parent context, which is large for big fan-outs.
