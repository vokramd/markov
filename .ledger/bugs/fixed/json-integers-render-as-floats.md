# Bug: JSON integers from http_request render as floats in templates

## Status

Fixed 2026-10-20. New `pkg/jsonvalue` decodes JSON with whole numbers as `int` (the rule `decodeState` already used for resumed state). Used by `http_request` bodies, `fromjson`/`from_json`, `set_fact` JSON parsing and `for_each` list parsing. Tests: `pkg/jsonvalue`, `TestHTTPRequestDecodesWholeNumbersAsInt`, `TestFromJSON_WholeNumbersRenderAsIntegers`; `TestFromJSONAliasParsesScalarInRenderMap` now expects `int`.

## Summary

`http_request` parses JSON bodies into Go `float64`, and a template renders such a number with six decimals, so an ID returned by an API cannot be put back into a URL without a filter.

## Reproduction

`topstories.json` returns `[50163277, ...]`; `url: "{{ base }}/item/{{ id }}.json"` in a `for_each` over it renders `/item/50163277.000000.json` (404).

## Expected

Whole numbers render as integers (`50163277`), as they appear in the JSON.

## Workaround

`{{ id | integer }}`.

## Impact

Any workflow that chains API calls by numeric ID. Agents writing workflows (the codification retro step) will hit it on the first try.
