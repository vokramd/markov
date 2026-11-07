# Bug: `markov validate` passes templates that don't parse

## Status

Fixed 2026-11-06.

- `template.Check` and `template.CheckExpr` (`pkg/template/template.go`) parse a template, or an
  expression the way `EvalBool` wraps it, without running it.
- `lintTemplates` (`pkg/parser/lint.go`) reports each of a step's templates and expressions that
  doesn't parse as an error. `walkStepText` lists them (step-type params included); `stepReads`
  now uses the same walker, so the two checks see the same text.
- Found by it: `examples/batch-rfe-pipeline.yaml` used a `ternary` filter, which doesn't exist;
  it is an `{% if %}` now. `template-engine.md` listed `truncate`, `capitalize` and `reverse`,
  none of which exist; they are `truncatechars`, `capfirst` and the `for … reversed` option.
- Docs: `cli.md` (markov validate), `template-engine.md` (filters).

Test: `TestLintTemplatesThatDoNotParse` (pkg/parser): an unknown filter in params and in a
`set_fact` value, an unknown tag, and a broken `when`.

## Summary

pongo2 finds unknown filters and tags when it parses a template, but templates were only parsed
when their step ran, so `validate` said `valid` and the run failed partway.

## Reproduction

markov-harness used `truncate` (from the template docs). `validate` passed; the step failed at
run time with `Filter 'truncate' does not exist`.
