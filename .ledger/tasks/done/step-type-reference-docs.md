# Task: Complete reference docs for every step type

## Status

Done (2026-11-04)

## Summary

An inventory of `docs/reference/step-types.md` against the 25 parser primitives found every type had a section, but with uneven depth, stale index pages, and drift the existing schema test could not see. Filled the gaps and extended the test so the same gaps fail CI.

## Changes

- `docs/concepts.md`: the step-types table lists all 25 types (it had 12).
- `docs/README.md`: the step-types link names all 25 (it named 9).
- `docs/reference/step-types.md`:
  - `jev`: renamed from `## Jev support` and given parameters, outputs, failure conditions and an example (it was a pointer to `jev.md`).
  - Failure-conditions sections for `script_exec`, `write_file`, `ansible`, `reload_workflow`, `codify`, `openshell_build`, `openshell_upload`, `openshell_download` and `openshell_delete`.
  - Examples for `openshell_build`, `openshell_create`, `openshell_upload`, `openshell_exec` and `openshell_download`, covering params the lifecycle example under `openshell_delete` does not.
  - `ansible`: an outputs table, a note that ad-hoc runs have no `recap`, and examples for map `module_args` and async `background`/`poll`.
  - `claude`: the `api_retries`, `last_api_error` and `sandbox_network` outputs.
  - `assert`: `msg` is used verbatim (see Open questions); the Jev functions are available; it registers nothing.
  - `codify`: a rejected candidate is not a step failure.
- `pkg/schema/schema.go`: `jev` lists `connection`, `state`, `questions`.
- `pkg/schema/schema_test.go`:
  - A type with no `## <type>` section fails (it was skipped).
  - Every section except `llm_invoke` needs `### Failure Conditions` and `### Example` headings.
  - New `TestParametersReadByExecutorsAreInTheSchema` scans executor sources for `params["x"]` and `f(params, "x")` and fails when a key is missing from the schema for that file's types (`executorFiles` maps files to types; an unmapped file that reads params fails too).
  - New `TestOverviewPagesListEveryStepType` checks `concepts.md` and `docs/README.md`.

## Verification

- `go test ./pkg/schema/`: the new tests failed first on exactly the gaps above (jev section and schema params, both index pages), then passed.
- Each new doc claim was checked against the executor or engine code (error paths, outputs, defaults).

## Open questions

- `assert` `msg` is not rendered (`pkg/engine/engine.go`, the `assert` branch uses `step.Msg` as is), while `parser/lint.go` checks it as a template and the old docs example used `{{ severity }}` in it. Docs now describe the actual behavior; rendering it is likely the intended fix.

## Limitations

- The code scan sees only `params["x"]` and `helper(params, "x")`. Keys read another way (decoded into a struct, read from a nested map such as `claude` `limits`) are not checked.
